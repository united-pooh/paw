"""进攻型牛市/震荡上行策略：高参与度趋势跟随与回撤容忍。"""

from __future__ import annotations

import math
from typing import Any


def backtest_aggressive_trend(
    rows: list[dict[str, Any]],
    fast: int = 10,
    slow: int = 30,
    breakout_window: int = 20,
    momentum_window: int = 10,
    exit_buffer: float = 0.02,
    max_weight: float = 1.0,
    fee_bp: float = 5.0,
    trailing_stop: float = 0.15,
    evaluation_start_date: str | None = None,
) -> dict[str, Any]:
    """回测追求牛市参与度的 long-only 进攻型趋势策略。

    价格站上慢均线且快均线向上时入场；突破近期高点或动量为正可确认入场。
    退出采用慢均线下破缓冲、快慢线转弱且动量转负，或较入场价的宽幅保护止损。
    默认不做波动率降仓，满足牛市中尽量保持高仓位的目标；仅用于研究模拟。
    """
    if not 2 <= fast < slow or breakout_window < 2 or momentum_window < 1:
        raise ValueError("需满足 2<=fast<slow、breakout_window>=2、momentum_window>=1")
    if exit_buffer < 0 or not 0 < max_weight <= 1 or trailing_stop <= 0 or fee_bp < 0:
        raise ValueError("exit_buffer、max_weight、trailing_stop 或 fee_bp 参数不合法")
    clean = sorted(
        [r for r in rows if r.get("open") is not None and r.get("close") is not None],
        key=lambda r: str(r.get("date", "")),
    )
    warmup = max(slow, breakout_window, momentum_window)
    if len(clean) <= warmup + 1:
        raise ValueError(f"至少需要 {warmup + 2} 条有效行情")

    closes = [float(r["close"]) for r in clean]
    opens = [float(r["open"]) for r in clean]
    start = warmup
    if evaluation_start_date is not None:
        start = next(
            (i for i, r in enumerate(clean) if str(r.get("date", "")) >= evaluation_start_date),
            len(clean),
        )
        if start >= len(clean) - 1:
            raise ValueError("evaluation_start_date 之后没有足够行情")

    equity = 1.0
    position = 0.0
    entry_price: float | None = None
    trades: list[dict[str, Any]] = []
    curve = [{"date": clean[start].get("date"), "equity": equity}]
    returns: list[float] = []
    total_cost = 0.0

    for i in range(start, len(clean) - 1):
        fast_ma = sum(closes[i - fast + 1 : i + 1]) / fast
        slow_ma = sum(closes[i - slow + 1 : i + 1]) / slow
        prior_high = max(closes[i - breakout_window : i])
        momentum = closes[i] / closes[i - momentum_window] - 1.0
        trend_up = fast_ma > slow_ma and closes[i] >= slow_ma
        breakout_or_momentum = closes[i] >= prior_high or momentum > 0
        enter = trend_up and breakout_or_momentum

        protected = entry_price is not None and closes[i] <= entry_price * (1 - trailing_stop)
        trend_break = closes[i] < slow_ma * (1 - exit_buffer)
        confirmation_break = fast_ma < slow_ma and momentum < 0
        exit_signal = position > 0 and (protected or trend_break or confirmation_break)
        desired = 0.0 if exit_signal else (max_weight if enter else position)

        fill = opens[i + 1]
        if abs(desired - position) > 1e-12:
            turnover = abs(desired - position)
            cost = turnover * fee_bp / 10000.0
            equity *= 1.0 - cost
            total_cost += cost
            action = "buy" if desired > position else "sell"
            trades.append({
                "date": clean[i + 1].get("date"),
                "action": action,
                "weight": round(desired, 6),
                "price": fill,
                "momentum": round(momentum, 5),
                "cost": cost,
            })
            if desired > 0 and position == 0:
                entry_price = fill
            elif desired == 0:
                entry_price = None
            position = desired

        day_ret = position * (closes[i + 1] / fill - 1.0) if fill else 0.0
        equity *= 1.0 + day_ret
        returns.append(day_ret)
        curve.append({"date": clean[i + 1].get("date"), "equity": equity})

    if position > 0:
        cost = position * fee_bp / 10000.0
        equity *= 1.0 - cost
        total_cost += cost
        trades.append({
            "date": clean[-1].get("date"), "action": "sell", "weight": 0.0,
            "price": closes[-1], "cost": cost, "reason": "end_of_test",
        })

    n = len(returns)
    mean = sum(returns) / n if n else 0.0
    std = math.sqrt(sum((x - mean) ** 2 for x in returns) / max(1, n - 1)) if n else 0.0
    sharpe = mean / std * math.sqrt(252) if std > 1e-12 else 0.0
    running = high = 1.0
    max_dd = 0.0
    for ret in returns:
        running *= 1.0 + ret
        high = max(high, running)
        max_dd = min(max_dd, running / high - 1.0)

    benchmark = closes[-1] / closes[start] - 1.0
    return {
        "strategy": "进攻型高参与度趋势突破 + 宽幅回撤保护",
        "period": {"start": clean[start].get("date"), "end": clean[-1].get("date")},
        "observations": n,
        "final_equity": round(equity, 6),
        "total_return_pct": round((equity - 1.0) * 100, 3),
        "benchmark_buy_hold_pct": round(benchmark * 100, 3),
        "annualized_sharpe": round(sharpe, 4),
        "max_drawdown_pct": round(max_dd * 100, 3),
        "trade_count": len(trades),
        "total_cost_pct": round(total_cost * 100, 4),
        "trades": trades,
        "equity_curve": curve,
        "note": "研究回测；默认高仓位参与牛市，承受较大回撤，未模拟涨跌停、冲击成本和真实成交队列。",
    }


__all__ = ["backtest_aggressive_trend"]
