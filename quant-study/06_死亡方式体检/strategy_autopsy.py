#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
菜六：量化系统"12 种死亡方式"自动体检器
========================================
对应学习内容：
  - 《AI量化交易从0到1》附录 B（12 种典型死亡方式 + 综合诊断表 + 每周健康检查清单）
  - 复用菜二（Deflated Sharpe Ratio）做过拟合尸检

用法：
  python3 strategy_autopsy.py                    # 演示：问题策略 vs 健康策略
  python3 strategy_autopsy.py --file returns.csv # 体检你自己的收益序列（一列日收益）
可选参数：
  --n-trials N        回测时试过的参数/策略组合数（过拟合检查，默认 1）
  --slippage-bp BP    每笔滑点（基点，执行失真检查，默认 1）
  --leverage L        杠杆倍数（爆仓检查，默认 1.0）
  --turnover T        日均换手率（0~1，用于估算滑点年化成本）

体检 12 项：✅ 健康 / ⚠️ 风险 / ❌ 病危 / N/A 无法自动检测（需外部数据）
"""

import sys
import os
import numpy as np

# 复用菜二：Deflated Sharpe Ratio（跨目录 import）
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "..", "02_反过拟合工具箱"))
from deflated_sharpe import deflated_sharpe_ratio, expected_max_sharpe  # noqa: E402

ANNUAL = 252
DAILY_LOSS_LIMIT = 0.03      # 附录B #5：单日亏损警戒线
DRAWDOWN_LIMIT = 0.20        # 附录B #5：回撤熔断警戒线
CRISIS_CORR = 0.80           # 附录B #7：危机相关性警戒线
MAX_SAFE_LEVERAGE_FRAC = 0.5 # 附录B #8：杠杆安全系数（建议<2倍）


# ----------------------------------------------------------------------------
# 单项检查
# ----------------------------------------------------------------------------

def check_data_pollution(rets: np.ndarray) -> dict:
    """死亡方式 #1：数据污染 —— 异常跳变 / 零收益（停牌/缺失）。"""
    std = rets.std(ddof=1)
    extreme = np.abs(rets - rets.mean()) > 6 * std
    zeros = np.abs(rets) < 1e-12
    n_ext = int(extreme.sum())
    n_zero = int(zeros.sum())
    issues = []
    if n_ext >= 1:
        # 6σ 日收益在 756 天里的期望出现次数 < 0.01，出现 1 个就极可疑
        issues.append(f"{n_ext} 个 |z|>6 的极端跳变（疑似价格除权错误/数据断点）")
    if n_zero > len(rets) * 0.05:
        issues.append(f"{n_zero} 天零收益（{n_zero / len(rets):.1%}，疑似停牌/缺失填充）")
    status = "❌" if issues else "✅"
    note = "；".join(issues) if issues else f"无异常（极端值 {n_ext} 个 / 零收益 {n_zero} 天）"
    return {"id": 1, "name": "数据污染型死亡", "status": status, "note": note,
            "fix": "数据质量检查管道 / 多源交叉验证 / 变更告警（附录B #1）"}


def check_overfit(rets: np.ndarray, n_trials: int) -> dict:
    """死亡方式 #2：过拟合 —— 用菜二 DSR 判死刑。"""
    sr = float(rets.mean() / rets.std(ddof=1)) if rets.std(ddof=1) > 0 else 0.0
    T = len(rets)
    var_sr = 1.0 / T                     # 高斯收益下 SR 估计方差的理论值
    skew = float(((rets - rets.mean()) ** 3).mean() / rets.std(ddof=1) ** 3)
    kurt = float(((rets - rets.mean()) ** 4).mean() / rets.std(ddof=1) ** 4)
    dsr = deflated_sharpe_ratio(sr, var_sr, n_trials, T, skew, kurt)
    need = None
    if n_trials > 1:
        # 反推：以同样 T/方差，需要多高的夏普才能 DSR≥0.95
        lo, hi = 0.0, 10.0
        for _ in range(60):
            mid = (lo + hi) / 2
            if deflated_sharpe_ratio(mid, var_sr, n_trials, T, skew, kurt) >= 0.95:
                hi = mid
            else:
                lo = mid
        need = (lo + hi) / 2 * np.sqrt(ANNUAL)
    status = "❌" if dsr < 0.95 else "✅"
    note = (f"年化夏普 {sr * np.sqrt(ANNUAL):.2f}，DSR={dsr:.3f}"
            + (f"（试过 {n_trials} 个组合，需年化 {need:.2f} 才达标）" if need else ""))
    return {"id": 2, "name": "过拟合型死亡", "status": status, "note": note,
            "fix": "严格 OOS / 限制参数 / 对完美回测保持怀疑（附录B #2）"}


def check_regime_drift(rets: np.ndarray) -> dict:
    """死亡方式 #3：Regime 漂移 —— 前后半段均值差异（近似 z 检验）。"""
    n = len(rets)
    half = n // 2
    a, b = rets[:half], rets[half:]
    diff = b.mean() - a.mean()
    se = np.sqrt(a.var(ddof=1) / len(a) + b.var(ddof=1) / len(b))
    z = diff / se if se > 0 else 0.0
    status = "❌" if z < -2.0 else ("⚠️" if z < -1.0 else "✅")
    note = (f"前半段年化 {a.mean() * ANNUAL * 100:+.1f}% → 后半段 {b.mean() * ANNUAL * 100:+.1f}%"
            f"（z={z:+.1f}）")
    return {"id": 3, "name": "Regime 漂移型死亡", "status": status, "note": note,
            "fix": "Regime Detection 模块 / 滚动相关性监控 / 多策略分散（附录B #3）"}


def check_execution(rets: np.ndarray, slippage_bp: float, turnover: float) -> dict:
    """死亡方式 #4：执行失真 —— 滑点成本占毛收益比。"""
    gross_ann = float(rets.mean() * ANNUAL)
    cost_ann = slippage_bp / 10000.0 * turnover * ANNUAL
    if gross_ann <= 0:
        status = "❌"
        note = (f"假设滑点 {slippage_bp}bp×换手 {turnover:.0%} → 年化成本 {cost_ann:.1%}"
                f"，毛收益为负（{gross_ann:.1%}），成本雪上加霜")
    else:
        ratio = cost_ann / gross_ann
        status = "❌" if ratio > 0.5 else ("⚠️" if ratio > 0.2 else "✅")
        note = (f"假设滑点 {slippage_bp}bp×换手 {turnover:.0%} → 年化成本 {cost_ann:.1%}"
                f"，占毛收益 {ratio * 100:.0f}%")
    return {"id": 4, "name": "执行失真型死亡", "status": status, "note": note,
            "fix": "保守成本假设 / Tick 回测 / 小资金实盘采样（附录B #4）"}


def check_risk_control(rets: np.ndarray) -> dict:
    """死亡方式 #5：风控失效 —— 单日亏损 / 最大回撤超警戒线。"""
    worst_day = float(rets.min())
    eq = np.cumprod(1 + rets)
    peak = np.maximum.accumulate(eq)
    max_dd = float((1 - eq / peak).max())
    issues = []
    if worst_day < -DAILY_LOSS_LIMIT:
        issues.append(f"单日最差 {worst_day:.1%} 超过 {DAILY_LOSS_LIMIT:.0%} 警戒线")
    if max_dd > DRAWDOWN_LIMIT:
        issues.append(f"最大回撤 {max_dd:.1%} 超过 {DRAWDOWN_LIMIT:.0%} 熔断线")
    status = "❌" if issues else "✅"
    note = "；".join(issues) if issues else f"单日最差 {worst_day:.1%} / 最大回撤 {max_dd:.1%}，均在线内"
    return {"id": 5, "name": "风控失效型死亡", "status": status, "note": note,
            "fix": "风控与策略独立 / 多层风控 / 风控不可绕过 / 定期演练（附录B #5）"}


def check_liquidity() -> dict:
    """死亡方式 #6：流动性枯竭 —— 需盘口数据，无法自动检测。"""
    return {"id": 6, "name": "流动性枯竭型死亡", "status": "N/A",
            "note": "需盘口深度/成交量数据：检查止损单能否成交、危机时滑点是否失控",
            "fix": "避免单标的集中 / 监控盘口深度 / 流动性压力测试（附录B #6）"}


def check_correlation(multi_asset: np.ndarray | None) -> dict:
    """死亡方式 #7：相关性飙升 —— 危机窗口 vs 全期相关性。"""
    if multi_asset is None or multi_asset.shape[1] < 2:
        return {"id": 7, "name": "相关性飙升型死亡", "status": "N/A",
                "note": "未提供多资产收益矩阵（需 2 列以上）",
                "fix": "压力测试用危机相关性 / 保留真不相关资产（附录B #7）"}
    X = multi_asset
    corr_all = np.corrcoef(X.T)
    n = len(X)
    # 危机窗口：组合收益最差的 20 天（附录B #7：危机时相关性飙升）
    port = X.mean(axis=1)
    idx = np.argsort(port)[:20]
    corr_crisis = np.corrcoef(X[idx].T)
    off_all = corr_all[np.triu_indices(corr_all.shape[0], 1)]
    off_crisis = corr_crisis[np.triu_indices(corr_crisis.shape[0], 1)]
    c_avg, cc_avg = float(off_all.mean()), float(off_crisis.mean())
    status = "❌" if cc_avg > CRISIS_CORR else ("⚠️" if cc_avg > 0.6 else "✅")
    note = f"全期平均相关 {c_avg:.2f} → 危机窗口 {cc_avg:.2f}"
    return {"id": 7, "name": "相关性飙升型死亡", "status": status, "note": note,
            "fix": "压力测试用危机相关性 / 保留真不相关资产（附录B #7）"}


def check_leverage(rets: np.ndarray, leverage: float) -> dict:
    """死亡方式 #8：杠杆爆仓 —— 用最大回撤反推安全杠杆。"""
    eq = np.cumprod(1 + rets)
    peak = np.maximum.accumulate(eq)
    max_dd = float((1 - eq / peak).max())
    safe_lev = 1.0 / max_dd * MAX_SAFE_LEVERAGE_FRAC if max_dd > 0 else np.inf
    status = "❌" if leverage > safe_lev else "✅"
    note = (f"杠杆 {leverage:.1f}x vs 最大回撤 {max_dd:.1%} 下的安全上限 {safe_lev:.1f}x"
            f"（回撤 {max_dd:.1%} × 杠杆 {leverage:.1f} = {leverage * max_dd:.1%} 本金）")
    return {"id": 8, "name": "杠杆爆仓型死亡", "status": status, "note": note,
            "fix": "杠杆上限<2x / 波动率调整杠杆 / 保证金缓冲 50%（附录B #8）"}


def check_human() -> dict:
    """死亡方式 #9：人为干预 —— 无法自动检测。"""
    return {"id": 9, "name": "人为干预型死亡", "status": "N/A",
            "note": "需操作日志：手动取消止损 / 亏损加仓 / 覆盖系统信号",
            "fix": "双人确认 / 干预留痕审批 / 干预胜率<50% 禁止干预（附录B #9）"}


def check_system() -> dict:
    """死亡方式 #10：系统故障 —— 无法自动检测。"""
    return {"id": 10, "name": "系统故障型死亡", "status": "N/A",
            "note": "需运维监控：订单发送失败 / 行情中断 / 延迟",
            "fix": "高可用主备切换 / 健康监控告警 / 安全模式只平仓（附录B #10）"}


def check_regulation() -> dict:
    """死亡方式 #11：监管变化 —— 无法自动检测。"""
    return {"id": 11, "name": "监管变化型死亡", "status": "N/A",
            "note": "需合规订阅：交易禁令 / 税收 / 保证金变化",
            "fix": "分散策略与地区 / 关注监管动态 / 预留缓冲期（附录B #11）"}


def check_adaptation(rets: np.ndarray) -> dict:
    """死亡方式 #12：对手盘适应 —— 滚动收益衰减（前后半段差异的 z 检验）。"""
    n = len(rets)
    win = 60
    rolling = np.array([rets[i:i + win].mean() for i in range(n - win)])
    t = np.arange(len(rolling))
    slope = float(np.polyfit(t, rolling, 1)[0]) * ANNUAL * 100   # 年化百分点的斜率/天
    first = rolling[:len(rolling) // 2].mean()
    last = rolling[len(rolling) // 2:].mean()
    # 滚动窗口重叠严重，有效独立样本 ≈ 窗口数/60
    se = rolling.std(ddof=1) / np.sqrt(len(rolling) / win)
    z = (last - first) / se if se > 0 else 0.0
    status = "❌" if z < -2.0 else ("⚠️" if z < -1.0 else "✅")
    note = (f"滚动60日收益斜率 {slope:+.2f}%/年·天；前半 {first * ANNUAL * 100:+.1f}%年化"
            f" → 后半 {last * ANNUAL * 100:+.1f}%年化（z={z:+.1f}）")
    return {"id": 12, "name": "对手盘适应型死亡", "status": status, "note": note,
            "fix": "分散信号 / 监控容量与边际收益 / 持续研发替换（附录B #12）"}


# ----------------------------------------------------------------------------
# 主体检
# ----------------------------------------------------------------------------

def autopsy(rets: np.ndarray, name: str = "策略", n_trials: int = 1,
            slippage_bp: float = 1.0, turnover: float = 0.1,
            leverage: float = 1.0, multi_asset: np.ndarray | None = None) -> list[dict]:
    print("=" * 76)
    print(f"  死亡方式体检报告：{name}")
    print("=" * 76)
    rets = np.asarray(rets, dtype=float)
    checks = [
        check_data_pollution(rets),
        check_overfit(rets, n_trials),
        check_regime_drift(rets),
        check_execution(rets, slippage_bp, turnover),
        check_risk_control(rets),
        check_liquidity(),
        check_correlation(multi_asset),
        check_leverage(rets, leverage),
        check_human(),
        check_system(),
        check_regulation(),
        check_adaptation(rets),
    ]
    print(f"  {'#':<3}{'死亡方式':<14}{'状态':<6}证据 / 预防措施")
    print(f"  {'—' * 72}")
    for c in checks:
        print(f"  {c['id']:<3}{c['name']:<14}{c['status']:<6}{c['note']}")
        if c["status"] in ("❌", "⚠️"):
            print(f"  {'':<24}→ 处方：{c['fix']}")
    # 汇总
    bad = [c for c in checks if c["status"] == "❌"]
    warn = [c for c in checks if c["status"] == "⚠️"]
    na = [c for c in checks if c["status"] == "N/A"]
    ok = [c for c in checks if c["status"] == "✅"]
    score = len(ok) / (len(checks) - len(na))
    print(f"  {'—' * 72}")
    print(f"  健康度：{score * 100:.0f}% ｜ ✅ {len(ok)} ｜ ⚠️ {len(warn)} ｜ ❌ {len(bad)} ｜ N/A {len(na)}")
    if bad:
        items = "、".join(f"#{c['id']} {c['name']}" for c in bad)
        print(f"  ⚰️ 病危项（优先级最高）：{items}")
    if warn:
        items = "、".join(f"#{c['id']} {c['name']}" for c in warn)
        print(f"  ⚠️ 风险项：{items}")
    print(f"  排查顺序（附录B综合诊断表）：数据→Regime→执行→风控→流动性→相关性→杠杆→人工→系统→监管→对手盘→过拟合")
    print()
    return checks


# ----------------------------------------------------------------------------
# 演示数据
# ----------------------------------------------------------------------------

def make_sick_strategy(seed: int = 7) -> np.ndarray:
    """问题策略：前半有 alpha，后半衰减（#12）；1 个数据污染点（#1）；危机段（#5/#8）。"""
    rng = np.random.default_rng(seed)
    n = 756  # 3 年
    rets = rng.normal(0.0010, 0.012, n)                    # 前半：年化 ~25%
    rets[n // 2:] += rng.normal(-0.0012, 0.012, n // 2)    # 后半：alpha 衰减到负
    rets[100] = -0.15                                      # 数据污染：除权错标
    rets[600:615] = rng.normal(-0.018, 0.02, 15)           # 危机段：连跌 15 天
    return rets


def make_healthy_strategy(seed: int = 8) -> np.ndarray:
    """健康策略：低换手、低回撤、无污染、无衰减。"""
    rng = np.random.default_rng(seed)
    n = 756
    rets = rng.normal(0.0008, 0.008, n)                    # 年化 ~20%
    return rets


def make_multi_asset(seed: int = 9, crisis: bool = True) -> np.ndarray:
    """多资产收益矩阵（供 #7 相关性检查）。crisis=True 时含公共因子危机段。"""
    rng = np.random.default_rng(seed)
    n = 756
    base = rng.normal(0.0004, 0.006, (n, 3))
    if crisis:
        common = rng.normal(-0.015, 0.025, 25)             # 公共因子：危机同涨同跌
        idios = rng.normal(0.0, 0.001, (25, 3))            # 个股特质部分很小
        base[596:621] = common[:, None] + idios            # 危机段 25 天
    return base


def demo() -> None:
    sick = make_sick_strategy()
    healthy = make_healthy_strategy()

    autopsy(sick, name="问题策略（3年，高频换手，试过200个参数）",
            n_trials=200, slippage_bp=5, turnover=0.5, leverage=1.0,
            multi_asset=make_multi_asset(crisis=True))
    autopsy(healthy, name="健康策略（3年，低换手，只试过1个参数）",
            n_trials=1, slippage_bp=1, turnover=0.05, leverage=1.0,
            multi_asset=make_multi_asset(crisis=False))


def main() -> None:
    if "--file" in sys.argv:
        idx = sys.argv.index("--file")
        data = np.loadtxt(sys.argv[idx + 1], delimiter=",")
        name = sys.argv[idx + 1]
        kwargs = {}
        for flag, cast in (("--n-trials", int), ("--slippage-bp", float),
                           ("--leverage", float), ("--turnover", float)):
            if flag in sys.argv:
                kwargs[flag[2:].replace("-", "_")] = cast(sys.argv[sys.argv.index(flag) + 1])
        autopsy(data, name=name, **kwargs)
    else:
        demo()


if __name__ == "__main__":
    main()
