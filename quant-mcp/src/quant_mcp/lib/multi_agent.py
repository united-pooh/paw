"""多智能体交易系统（第11课架构 + 第12课Regime + 第15课风控 + 第19课执行）。"""

import numpy as np

INIT_CAPITAL = 1_000_000.0
SLIPPAGE = 0.001
DAILY_LOSS_LIMIT = 0.025
DRAWDOWN_LIMIT = 0.12
MAX_POSITION = 0.9
CRISIS_POSITION = 0.2


def generate_market(n_days: int = 500, seed: int = 2026) -> tuple[np.ndarray, np.ndarray]:
    rng = np.random.default_rng(seed)
    segments = [
        (0, 120, 0.0025, 0.010, "趋势牛"),
        (120, 240, 0.0003, 0.008, "震荡"),
        (240, 300, -0.025, 0.030, "危机"),
        (300, 400, 0.0008, 0.010, "震荡"),
        (400, 500, -0.0008, 0.012, "趋势熊"),
    ]
    rets, true = [], []
    for start, end, mu, vol, name in segments:
        n = end - start
        rets.append(rng.normal(mu, vol, n))
        true.extend([name] * n)
    return np.concatenate(rets), np.array(true)


class RegimeAgent:
    def __init__(self, lookback: int = 20):
        self.lb = lookback
        self.state = "震荡"

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
        return self.state

    def vote(self) -> int:
        return {"趋势牛": 1, "趋势熊": -1, "震荡": 0, "危机": -2}[self.state]


class SignalAgent:
    def __init__(self, lookback: int = 20):
        self.lb = lookback

    def target_position(self, prices: np.ndarray, i: int, regime: str) -> float:
        p = prices
        ret20 = p[i] / p[i - self.lb] - 1.0
        ma20 = float(np.mean(p[i - self.lb:i + 1]))
        z = (p[i] - ma20) / ma20
        if regime == "震荡":
            pos = np.clip(-z * 8.0, -0.5, 0.5)
        elif regime == "趋势牛":
            pos = np.clip(ret20 * 15.0 + 0.2, 0.0, 1.0)
        elif regime == "趋势熊":
            pos = np.clip(ret20 * 15.0 - 0.2, -1.0, 0.0)
        else:
            pos = 0.0
        return float(pos)


class RiskAgent:
    """FinMem 式教训记忆：去重，吃过亏后危机直接空仓。"""

    def __init__(self):
        self.lessons = []
        self.vetoes = {"单日亏损": 0, "回撤": 0, "集中度": 0, "危机降仓": 0}

    def remember(self, lesson: str) -> None:
        if lesson not in self.lessons:
            self.lessons.append(lesson)

    def review(self, pos: float, daily_ret: float, drawdown: float,
               regime: str) -> tuple[float, str | None]:
        new_pos = pos
        reason = None
        crisis_cap = CRISIS_POSITION
        if any("回撤" in l or "亏损" in l for l in self.lessons):
            crisis_cap = 0.0
        if regime == "危机":
            new_pos = min(new_pos, crisis_cap)
            reason = "危机降仓"
            self.vetoes["危机降仓"] += 1
        if daily_ret < -DAILY_LOSS_LIMIT and pos > 0:
            new_pos = 0.0
            reason = "单日亏损熔断"
            self.vetoes["单日亏损"] += 1
            self.remember("单日亏损超限: 应空仓避险")
        if drawdown > DRAWDOWN_LIMIT:
            new_pos = min(new_pos, 0.3)
            reason = "回撤熔断"
            self.vetoes["回撤"] += 1
            self.remember("回撤超限: 应更早降仓")
        if abs(new_pos) > MAX_POSITION:
            new_pos = np.sign(new_pos) * MAX_POSITION
            reason = "集中度限制"
            self.vetoes["集中度"] += 1
        return new_pos, reason


class MetaAgent:
    @staticmethod
    def arbitrate(signal_pos: float, regime_vote: int, use_risk: bool) -> float:
        if regime_vote == -2:
            return 0.0 if use_risk else signal_pos
        if regime_vote == 0:
            return signal_pos * 0.5
        if np.sign(signal_pos) != np.sign(regime_vote):
            return signal_pos * 0.3
        return signal_pos


def simulate(use_risk: bool = True, seed: int = 2026, verbose: bool = False) -> dict:
    """跑 500 天多智能体决策循环，返回绩效指标与风控事件。"""
    prices, true_regime = generate_market(seed=seed)
    n = len(prices)
    regime_agent = RegimeAgent()
    signal_agent = SignalAgent()
    risk_agent = RiskAgent()

    pos = 0.0
    equity = np.full(n, INIT_CAPITAL)
    peak = INIT_CAPITAL
    max_dd = 0.0
    trades = 0
    veto_count = 0
    journal = []

    for i in range(20, n):
        regime = regime_agent.update(prices, i)
        target = signal_agent.target_position(prices, i, regime)
        target = MetaAgent.arbitrate(target, regime_agent.vote(), use_risk)
        ret_today = prices[i] / prices[i - 1] - 1.0
        drawdown = 1.0 - equity[i - 1] / peak
        if use_risk:
            target, veto = risk_agent.review(target, ret_today, drawdown, regime)
            if veto:
                veto_count += 1
                journal.append({"day": int(i), "regime": regime, "veto": veto,
                                "pos": round(float(target), 3)})
        equity[i] = equity[i - 1] * (1.0 + pos * ret_today)
        equity[i] -= abs(target - pos) * SLIPPAGE * INIT_CAPITAL
        pos = target
        if abs(pos) > 1e-9:
            trades += 1
        peak = max(peak, equity[i])
        max_dd = max(max_dd, 1.0 - equity[i] / peak)

    daily = np.diff(equity) / equity[:-1]
    sharpe = float(daily.mean() / daily.std(ddof=1) * np.sqrt(252)) if daily.std(ddof=1) > 0 else 0.0
    return {
        "use_risk": use_risk,
        "total_return": round(float(equity[-1] / INIT_CAPITAL - 1.0), 4),
        "annualized_sharpe": round(sharpe, 3),
        "max_drawdown": round(max_dd, 4),
        "trades": trades,
        "vetoes": veto_count,
        "lessons": risk_agent.lessons if use_risk else [],
        "veto_events": journal[:8],
        "regime_share": {r: int((true_regime == r).sum()) for r in
                         ["趋势牛", "震荡", "危机", "趋势熊"]},
    }
