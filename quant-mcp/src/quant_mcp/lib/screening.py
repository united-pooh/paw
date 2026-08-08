"""五因子选股/ETF评分：只依赖标准化日线数据，便于单元测试。"""

from __future__ import annotations

import math
from typing import Any


FACTOR_WEIGHTS = {
    "momentum_60": 0.25,
    "momentum_20": 0.15,
    "ma20_deviation": 0.20,
    "volatility": 0.20,
    "ma60_trend": 0.20,
}


def _clamp(value: float, low: float = 0.0, high: float = 100.0) -> float:
    return max(low, min(high, value))


def _linear(value: float, low: float, high: float) -> float:
    if high <= low:
        return 50.0
    return _clamp((value - low) / (high - low) * 100.0)


def _mean(values: list[float]) -> float:
    return sum(values) / len(values) if values else 0.0


def _std(values: list[float]) -> float:
    if len(values) < 2:
        return 0.0
    mean = _mean(values)
    return math.sqrt(sum((x - mean) ** 2 for x in values) / (len(values) - 1))


def _factor_scores(momentum_60: float, momentum_20: float, deviation: float,
                   volatility: float, trend: float) -> dict[str, float]:
    # 适度正动量优于追高；过度偏离均线会扣分。波动率是风险因子，越低越好。
    scores = {
        "momentum_60": _linear(momentum_60, -0.30, 0.30),
        "momentum_20": _linear(momentum_20, -0.15, 0.15),
        "ma20_deviation": (
            _linear(deviation, -0.05, 0.08) if deviation <= 0.08
            else _clamp(100.0 - (deviation - 0.08) / 0.22 * 70.0)
        ),
        "volatility": _clamp(100.0 - _linear(volatility, 0.10, 0.65)),
        "ma60_trend": 100.0 if trend > 0 else 0.0,
    }
    return {key: round(value, 2) for key, value in scores.items()}


def score_rows(rows: list[dict[str, Any]], symbol: str = "") -> dict[str, Any]:
    """对至少 61 根日线收盘价计算五因子评分。"""
    closes = [float(row["close"]) for row in rows if row.get("close") is not None]
    if len(closes) < 61:
        raise ValueError(f"{symbol or '标的'} 日线数据不足，至少需要 61 根，实际 {len(closes)} 根")
    returns = [closes[i] / closes[i - 1] - 1.0 for i in range(1, len(closes))]
    latest = closes[-1]
    ma20 = _mean(closes[-20:])
    ma60 = _mean(closes[-60:])
    momentum_20 = latest / closes[-21] - 1.0
    momentum_60 = latest / closes[-61] - 1.0
    deviation = latest / ma20 - 1.0 if ma20 else 0.0
    annualized_volatility = _std(returns[-20:]) * math.sqrt(252.0)
    trend = 1.0 if latest > ma60 and ma20 > ma60 else 0.0
    factors = _factor_scores(momentum_60, momentum_20, deviation,
                             annualized_volatility, trend)
    total = sum(factors[name] * weight for name, weight in FACTOR_WEIGHTS.items())
    latest_date = rows[-1].get("date") if rows else None
    return {
        "symbol": symbol,
        "score": round(total, 1),
        "rating": "强势" if total >= 70 else "中性" if total >= 50 else "偏弱",
        "factors": factors,
        "metrics": {
            "latest_close": round(latest, 6),
            "momentum_60_pct": round(momentum_60 * 100, 2),
            "momentum_20_pct": round(momentum_20 * 100, 2),
            "ma20": round(ma20, 6),
            "ma60": round(ma60, 6),
            "ma20_deviation_pct": round(deviation * 100, 2),
            "annualized_volatility_pct": round(annualized_volatility * 100, 2),
            "trend_up": bool(trend),
        },
        "latest_date": latest_date,
        "observations": len(closes),
        "factor_weights": FACTOR_WEIGHTS.copy(),
        "note": "分数是规则化研究指标，不是收益承诺；过度追高和高波动会扣分。",
    }
