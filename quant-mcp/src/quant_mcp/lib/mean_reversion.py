"""震荡与高波动适配策略：均值回归、反转确认与波动率门控。"""

from __future__ import annotations

import math
from typing import Any


def _sma(values: list[float], window: int) -> float:
    return sum(values[-window:]) / window


def _stdev(values: list[float], window: int) -> float:
    xs = values[-window:]
    mean = sum(xs) / len(xs)
    return math.sqrt(sum((x - mean) ** 2 for x in xs) / max(1, len(xs) - 1))


def _clean(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out = [r for r in rows if r.get("open") is not None and r.get("close") is not None]
    return sorted(out, key=lambda r: str(r.get("date", "")))


def backtest_mean_reversion(
    rows: list[dict[str, Any]],
    lookback: int = 20,
    entry_z: float = -1.5,
    exit_z: float = -0.1,
    max_hold_days: int = 10,
    vol_window: int = 20,
    max_annual_vol: float = 0.65,
    target_vol: float = 0.15,
    max_weight: float = 0.5,
    fee_bp: float = 5.0,
    evaluation_start_date: str | None = None,
) -> dict[str, Any]:
    """回测适配震荡/高波动的 long-only 均值回归策略。

    价格低于布林中轨且 z-score 足够低时，次日开盘买入；回到中轨附近、
    超过持有期限或波动率过高时退出。为避免下跌趋势接飞刀，只有收盘价
    高于短期均线，或最近3日出现反弹确认时才允许入场。
    """
    if lookback < 5 or vol_window < 2 or not entry_z < exit_z <= 1:
        raise ValueError("参数需满足 lookback>=5、vol_window>=2、entry_z<exit_z<=1")
    if max_hold_days < 1 or not 0 < max_annual_vol <= 3 or not 0 < target_vol <= 2:
        raise ValueError("持有期、波动率参数不合法")
    clean = _clean(rows)
    warmup = max(lookback, vol_window) + 3
    if len(clean) < warmup + 2:
        raise ValueError(f"至少需要 {warmup + 2} 条有效行情")
    closes = [float(r["close"]) for r in clean]
    opens = [float(r["open"]) for r in clean]
    start = warmup
    if evaluation_start_date:
        start = next((i for i, r in enumerate(clean) if str(r.get("date", "")) >= evaluation_start_date), len(clean))
        if start >= len(clean) - 1:
            raise ValueError("evaluation_start_date 之后没有足够行情")
    equity = 1.0
    position = 0.0
    entry_i: int | None = None
    trades: list[dict[str, Any]] = []
    curve = [{"date": clean[start].get("date"), "equity": equity}]
    returns: list[float] = []
    total_cost = 0.0

    for i in range(start, len(clean) - 1):
        mean = _sma(closes[: i + 1], lookback)
        sd = _stdev(closes[: i + 1], lookback)
        z = (closes[i] - mean) / sd if sd > 1e-12 else 0.0
        rets = [closes[j] / closes[j - 1] - 1 for j in range(i - vol_window + 1, i + 1)]
        daily_vol = math.sqrt(sum((x - sum(rets) / len(rets)) ** 2 for x in rets) / max(1, len(rets) - 1))
        annual_vol = daily_vol * math.sqrt(252)
        weight = min(max_weight, target_vol / annual_vol) if annual_vol > 1e-9 else max_weight
        # 反转确认：不在连续下跌中盲目接刀；高波动仍允许，但严格压低仓位/过滤极端波动。
        rebound = closes[i] > closes[i - 1] and closes[i] > closes[i - 3]
        short_ok = closes[i] >= _sma(closes[: i + 1], min(5, lookback)) or rebound
        enter = z <= entry_z and annual_vol <= max_annual_vol and short_ok
        held = i - entry_i if entry_i is not None else 0
        exit_signal = position > 0 and (z >= exit_z or held >= max_hold_days or annual_vol > max_annual_vol)
        desired = 0.0 if exit_signal else (weight if enter else position)
        fill = opens[i + 1]
        if abs(desired - position) > 1e-12:
            turnover = abs(desired - position)
            cost = turnover * fee_bp / 10000
            equity *= 1 - cost
            total_cost += cost
            action = "buy" if desired > position else "sell"
            trades.append({"date": clean[i + 1].get("date"), "action": action, "weight": round(desired, 6), "price": fill, "z": round(z, 3), "annual_vol": round(annual_vol, 4), "cost": cost})
            if desired > 0 and position == 0:
                entry_i = i
            if desired == 0:
                entry_i = None
            position = desired
        day_ret = position * (closes[i + 1] / fill - 1) if fill else 0.0
        equity *= 1 + day_ret
        returns.append(day_ret)
        curve.append({"date": clean[i + 1].get("date"), "equity": equity})

    if position > 0:
        cost = position * fee_bp / 10000
        equity *= 1 - cost
        total_cost += cost
        trades.append({"date": clean[-1].get("date"), "action": "sell", "weight": 0.0, "price": closes[-1], "cost": cost, "reason": "end_of_test"})
    mean_ret = sum(returns) / len(returns) if returns else 0.0
    sd_ret = math.sqrt(sum((x - mean_ret) ** 2 for x in returns) / max(1, len(returns) - 1)) if returns else 0.0
    sharpe = mean_ret / sd_ret * math.sqrt(252) if sd_ret > 1e-12 else 0.0
    running = high = 1.0
    max_dd = 0.0
    for ret in returns:
        running *= 1 + ret
        high = max(high, running)
        max_dd = min(max_dd, running / high - 1)
    benchmark = closes[-1] / closes[start] - 1
    return {
        "strategy": "Bollinger均值回归 + 反弹确认 + 波动率门控",
        "period": {"start": clean[start].get("date"), "end": clean[-1].get("date")},
        "observations": len(returns), "final_equity": round(equity, 6),
        "total_return_pct": round((equity - 1) * 100, 3),
        "benchmark_buy_hold_pct": round(benchmark * 100, 3),
        "annualized_sharpe": round(sharpe, 4), "max_drawdown_pct": round(max_dd * 100, 3),
        "trade_count": len(trades), "total_cost_pct": round(total_cost * 100, 4),
        "trades": trades, "equity_curve": curve,
        "note": "研究回测；long-only，A股下跌高波动阶段仍可能亏损，未模拟涨跌停和冲击成本。",
    }


__all__ = ["backtest_mean_reversion"]
