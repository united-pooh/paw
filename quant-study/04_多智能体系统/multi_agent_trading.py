#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
菜四：多智能体交易系统骨架（第11课架构 + 第19课执行成本 + LLM Agent 记忆思想）
================================================================================
对应学习内容：
  - 《AI量化交易从0到1》第 11 课（为什么需要多智能体：分工/投票/一票否决）
  - 第 12 课（Regime Agent：市场状态识别）、第 15 课（Risk Agent：一票否决）
  - 第 19 课（Execution Agent：滑点/冲击成本）
  - arXiv:2309.03736 TradingGPT（分层记忆）、arXiv:2311.13743 FinMem（盈亏驱动记忆）

系统角色（第11课责任边界表）：
  Data Agent       → 行情数据（合成数据生成器）
  Regime Agent     → 识别 {趋势牛, 震荡, 趋势熊, 危机}，投方向票
  Signal Agent     → 动量 + 均值回归，输出目标仓位 [-1, 1]
  Risk Agent       → 一票否决：单日亏损熔断 / 回撤熔断 / 集中度限制 / 危机强制降仓
  Execution Agent  → 按滑点成本成交（第19课：显示价 ≠ 成交价）
  Meta Agent       → 投票仲裁 + 汇总决策

设计要点：
  - Risk Agent 带 FinMem 式"盈亏驱动记忆"：危机中吃过亏后，下次危机自动更早降仓
  - 支持 --no-risk 运行对照实验：展示"一票否决"的价值（第11课方案3）

仅依赖 numpy，Python >= 3.8。
用法：
  python3 multi_agent_trading.py             # 完整多智能体（含风控）
  python3 multi_agent_trading.py --no-risk   # 对照组：关闭 Risk Agent
"""

import sys
import numpy as np

# ----------------------------------------------------------------------------
# 参数
# ----------------------------------------------------------------------------
INIT_CAPITAL = 1_000_000.0
SLIPPAGE = 0.001          # 第19课：每单位换仓的滑点成本（显示价≠成交价）
DAILY_LOSS_LIMIT = 0.025  # 单日亏损超 2.5% → 否决新开仓
DRAWDOWN_LIMIT = 0.12     # 回撤超 12% → 强制降仓
MAX_POSITION = 0.9        # 仓位上限（集中度）
CRISIS_POSITION = 0.2     # 危机时强制仓位


# ----------------------------------------------------------------------------
# Data Agent：合成行情（含趋势牛/震荡/危机/趋势熊 四个 regime）
# ----------------------------------------------------------------------------
def generate_market(n_days: int = 500, seed: int = 2026) -> tuple[np.ndarray, np.ndarray]:
    rng = np.random.default_rng(seed)
    rets = []
    segments = [
        (0, 120, 0.0025, 0.010, "趋势牛"),      # 强牛市：让系统有肉可吃
        (120, 240, 0.0003, 0.008, "震荡"),
        (240, 300, -0.025, 0.030, "危机"),      # 连续暴跌段（考验风控）
        (300, 400, 0.0008, 0.010, "震荡"),
        (400, 500, -0.0008, 0.012, "趋势熊"),
    ]
    true_regime = []
    for start, end, mu, vol, name in segments:
        n = end - start
        rets.append(rng.normal(mu, vol, n))
        true_regime.extend([name] * n)
    rets = np.concatenate(rets)
    prices = 100.0 * np.exp(np.cumsum(rets))
    return prices, np.array(true_regime)


# ----------------------------------------------------------------------------
# Agent 们（第 11 课责任边界：每个 Agent 只做一件事）
# ----------------------------------------------------------------------------

class RegimeAgent:
    """第12课：市场状态识别。只输出状态，不输出交易决策。"""
    def __init__(self, lookback: int = 20):
        self.lb = lookback
        self.state = "震荡"
        self.history = []

    def update(self, prices: np.ndarray, i: int) -> str:
        p = prices
        ret20 = p[i] / p[i - self.lb] - 1.0
        vol20 = float(np.std(np.diff(p[i - self.lb:i + 1]) / p[i - self.lb:i], ddof=1))
        dd10 = p[i] / np.max(p[i - 10:i + 1]) - 1.0
        if dd10 < -0.08 or (vol20 > 0.025 and ret20 < -0.05):
            self.state = "危机"
        elif ret20 > 0.05:
            self.state = "趋势牛"
        elif ret20 < -0.05:
            self.state = "趋势熊"
        else:
            self.state = "震荡"
        self.history.append(self.state)
        return self.state

    def vote(self) -> int:
        """方向票：+1 看多 / -1 看空 / 0 观望 / 危机 = -2（强制避险）"""
        return {"趋势牛": 1, "趋势熊": -1, "震荡": 0, "危机": -2}[self.state]


class SignalAgent:
    """信号 Agent：动量 + 均值回归，按 Regime 调整权重。输出目标仓位。"""
    def __init__(self, lookback: int = 20):
        self.lb = lookback

    def target_position(self, prices: np.ndarray, i: int, regime: str) -> float:
        p = prices
        ret20 = p[i] / p[i - self.lb] - 1.0
        ma20 = float(np.mean(p[i - self.lb:i + 1]))
        z = (p[i] - ma20) / ma20                      # 价格偏离均线程度
        if regime == "震荡":
            # 均值回归：超买卖空、超卖买多
            pos = np.clip(-z * 8.0, -0.5, 0.5)
        elif regime == "趋势牛":
            pos = np.clip(ret20 * 15.0 + 0.2, 0.0, 1.0)   # 顺势做多
        elif regime == "趋势熊":
            pos = np.clip(ret20 * 15.0 - 0.2, -1.0, 0.0)  # 顺势做空
        else:  # 危机
            pos = 0.0
        return float(pos)

    def vote(self, pos: float) -> int:
        return 1 if pos > 0.05 else (-1 if pos < -0.05 else 0)


class RiskAgent:
    """
    第15课 + 一票否决（第11课方案3）。
    带 FinMem 式盈亏驱动记忆：把'危机教训'记下来，下次危机更早降仓。
    """
    def __init__(self):
        self.lessons = []          # FinMem 式教训记忆（去重 + 盈亏驱动剪枝）
        self.vetoes = {"单日亏损": 0, "回撤": 0, "集中度": 0, "危机降仓": 0}

    def remember(self, lesson: str) -> None:
        """FinMem 式记忆写入：同样教训只记一次（剪枝冗余），教训越多风控越早。"""
        if lesson not in self.lessons:
            self.lessons.append(lesson)

    def review(self, pos: float, daily_ret: float, drawdown: float,
               regime: str) -> tuple[float, str]:
        """返回 (允许仓位, 否决原因)。None 原因 = 放行。"""
        new_pos = pos
        reason = None

        # 记忆增强：危机中吃过亏（教训），危机降仓阈值更狠
        crisis_cap = CRISIS_POSITION
        if any("回撤" in l or "亏损" in l for l in self.lessons):
            crisis_cap = 0.0     # 吃过亏 → 危机时直接空仓

        if regime == "危机":
            new_pos = min(new_pos, crisis_cap)
            reason = "危机降仓"
            self.vetoes["危机降仓"] += 1
        if daily_ret < -DAILY_LOSS_LIMIT and pos > 0:
            new_pos = 0.0
            reason = "单日亏损熔断"
            self.vetoes["单日亏损"] += 1
            self.remember("单日亏损超限: 应空仓避险")          # FinMem：亏钱 → 记住教训
        if drawdown > DRAWDOWN_LIMIT:
            new_pos = min(new_pos, 0.3)
            reason = "回撤熔断"
            self.vetoes["回撤"] += 1
            self.remember("回撤超限: 应更早降仓")              # FinMem：教训入库（去重）
        if abs(new_pos) > MAX_POSITION:
            new_pos = np.sign(new_pos) * MAX_POSITION
            reason = "集中度限制"
            self.vetoes["集中度"] += 1
        return new_pos, reason


class ExecutionAgent:
    """第19课：显示价 ≠ 成交价。滑点 = SLIPPAGE × |换仓量|。"""
    @staticmethod
    def fill_cost(old_pos: float, new_pos: float) -> float:
        return abs(new_pos - old_pos) * SLIPPAGE * INIT_CAPITAL


class MetaAgent:
    """第11课仲裁：Signal + Regime 投票，Risk 一票否决。"""
    @staticmethod
    def arbitrate(signal_pos: float, regime_vote: int) -> float:
        if regime_vote == -2:            # 危机：Regime 一票避险
            return 0.0
        if regime_vote == 0:             # 震荡：信号减半（降低换手，第19课摩擦意识）
            return signal_pos * 0.5
        if np.sign(signal_pos) != np.sign(regime_vote) and regime_vote != 0:
            return signal_pos * 0.3      # 逆势：大幅降权
        return signal_pos


# ----------------------------------------------------------------------------
# 模拟主循环
# ----------------------------------------------------------------------------

def run_simulation(use_risk: bool = True, verbose: bool = True) -> dict:
    prices, true_regime = generate_market()
    n = len(prices)

    regime_agent = RegimeAgent()
    signal_agent = SignalAgent()
    risk_agent = RiskAgent()
    meta_agent = MetaAgent()

    capital = INIT_CAPITAL
    pos = 0.0
    equity = np.full(n, INIT_CAPITAL)   # 前 20 天未开跑，保持初始资金
    peak = INIT_CAPITAL
    max_dd = 0.0
    trades = 0
    veto_count = 0
    journal = []

    for i in range(20, n):
        # 1. Data Agent → Regime Agent
        regime = regime_agent.update(prices, i)

        # 2. Signal Agent 给目标仓位
        target = signal_agent.target_position(prices, i, regime)

        # 3. Meta Agent 投票仲裁
        target = meta_agent.arbitrate(target, regime_agent.vote())

        # 4. Risk Agent 一票否决（用昨日净值算回撤，避免未来函数）
        ret_today = prices[i] / prices[i - 1] - 1.0
        drawdown = 1.0 - equity[i - 1] / peak
        if use_risk:
            target, veto = risk_agent.review(target, ret_today, drawdown, regime)
            if veto:
                veto_count += 1
                journal.append((i, regime, veto, target))
        else:
            veto = None

        # 5. 当日盈亏：用【昨日】仓位 × 今日收益（先记账，再换仓）
        equity[i] = equity[i - 1] * (1.0 + pos * ret_today)

        # 6. Execution Agent 成交（滑点成本从净值中扣除）
        cost = ExecutionAgent.fill_cost(pos, target)
        equity[i] -= cost
        pos = target
        if abs(pos) > 1e-9:
            trades += 1

        peak = max(peak, equity[i])
        max_dd = max(max_dd, 1.0 - equity[i] / peak)

    # 摘要
    total_ret = equity[-1] / INIT_CAPITAL - 1.0
    daily_rets = np.diff(equity) / equity[:-1]
    sharpe = float(daily_rets.mean() / daily_rets.std(ddof=1) * np.sqrt(252)) \
        if daily_rets.std(ddof=1) > 0 else 0.0

    return {
        "total_ret": total_ret, "sharpe": sharpe, "max_dd": max_dd,
        "trades": trades, "vetoes": veto_count,
        "equity": equity, "true_regime": true_regime, "journal": journal,
        "risk": use_risk, "lessons": risk_agent.lessons if use_risk else [],
    }


# ----------------------------------------------------------------------------
# 演示
# ----------------------------------------------------------------------------

def print_summary(res: dict) -> None:
    tag = "多智能体（含 Risk 一票否决）" if res["risk"] else "对照组（无 Risk Agent）"
    print(f"  [{tag}]")
    print(f"    总收益        : {res['total_ret'] * 100:>8.1f}%")
    print(f"    年化夏普      : {res['sharpe']:>8.2f}")
    print(f"    最大回撤      : {res['max_dd'] * 100:>8.1f}%")
    print(f"    交易次数      : {res['trades']:>8d}")
    if res["risk"]:
        print(f"    风控否决次数  : {res['vetoes']:>8d}")
        print(f"    Risk 记忆教训 : {res['lessons'] if res['lessons'] else '（暂无）'}")


def print_journal_sample(res: dict) -> None:
    print("  风控否决事件摘录（第11课：一票否决 = 资金最后防线）：")
    # 优先展示有代表性的否决（熔断/危机），其次才展示集中度
    priority = [e for e in res["journal"] if e[2] in ("回撤熔断", "危机降仓", "单日亏损熔断")]
    rest = [e for e in res["journal"] if e not in priority]
    shown = 0
    for i, regime, veto, target in priority[:4] + rest[:2]:
        print(f"    第 {i:>3} 天 [真实:{res['true_regime'][i]}/{regime}] "
              f"→ {veto} → 仓位压到 {target:.2f}")
        shown += 1
    if shown == 0:
        print("    （本局没有触发否决）")


def demo() -> None:
    print()
    print("  多智能体交易系统骨架（第11课架构 + 第12课Regime + 第15课风控 + 第19课执行）")
    print("  行情：500 天合成数据（趋势牛→震荡→危机→震荡→趋势熊）\n")
    print("=" * 76)
    print("实验 1：完整多智能体（Signal + Regime + Risk 一票否决 + Execution）")
    print("=" * 76)
    full = run_simulation(use_risk=True)
    print_summary(full)
    print_journal_sample(full)
    print()
    print("=" * 76)
    print("实验 2：对照组（关闭 Risk Agent —— 第11课开头'全能Agent'式单脑系统）")
    print("=" * 76)
    no_risk = run_simulation(use_risk=False)
    print_summary(no_risk)
    print()
    print("=" * 76)
    print("对比结论（第11课：专业分工 + 一票否决）：")
    print("=" * 76)
    print(f"  总收益 : 多智能体 {full['total_ret'] * 100:+.1f}%  vs  无风控 {no_risk['total_ret'] * 100:+.1f}%")
    print(f"  最大回撤: 多智能体 {full['max_dd'] * 100:.1f}%  vs  无风控 {no_risk['max_dd'] * 100:.1f}%")
    print(f"  夏普   : 多智能体 {full['sharpe']:.2f}  vs  无风控 {no_risk['sharpe']:.2f}")
    print("""
  解读：
  · Regime Agent 在危机段投票避险（-2），Signal 顺势信号在熊市被降权（0.3x）
  · Risk Agent 一票否决：单日亏损熔断、回撤熔断、危机强制降仓
  · 无风控对照组：信号照单全收，危机段硬扛 → 回撤大幅放大
  · Execution Agent 按滑点扣成本 → 震荡市信号减半，避免'手续费收割机'（第19课）
  · Risk Agent 的 FinMem 式记忆：吃过一次亏后，下次危机直接空仓
  （把第2309.03736/2311.13743 的分层记忆思想塞进了风控 Agent）""")
    print("=" * 76)


if __name__ == "__main__":
    if "--no-risk" in sys.argv:
        r = run_simulation(use_risk=False)
        print_summary(r)
    else:
        demo()
