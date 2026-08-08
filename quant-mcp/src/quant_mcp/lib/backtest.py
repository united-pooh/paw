"""可解释的日频趋势策略回测：信号收盘确认，次日开盘成交。"""

from __future__ import annotations

import math
from typing import Any


def backtest_trend(
    rows: list[dict[str, Any]],
    fast: int = 20,
    slow: int = 60,
    vol_window: int = 20,
    target_vol: float = 0.15,
    max_weight: float = 1.0,
    fee_bp: float = 5.0,
    stop_loss: float = 0.08,
    momentum_window: int = 0,
    momentum_threshold: float = 0.0,
    evaluation_start_date: str | None = None,
) -> dict[str, Any]:
    """回测单个 ETF 的 long/cash 趋势策略。

    每日收盘计算 MA/波动率，信号在下一交易日开盘执行；仓位按历史波动率缩放，
    只允许 0~max_weight，持仓从收盘跌破慢均线或相对入场价触发止损时退出。
    fee_bp 按换仓名义金额计，避免使用未来数据。
    """
    if not 2 <= fast < slow or vol_window < 2:
        raise ValueError("需满足 2<=fast<slow，且 vol_window>=2")
    if momentum_window < 0:
        raise ValueError("momentum_window 必须 >= 0")
    if momentum_window and momentum_window >= len(rows):
        raise ValueError("momentum_window 必须小于行情长度")
    if momentum_threshold < -1.0:
        raise ValueError("momentum_threshold 不能小于 -1")
    if not rows or len(rows) <= slow + 2:
        raise ValueError(f"至少需要 {slow + 3} 条有效行情")
    clean = [r for r in rows if r.get("open") is not None and r.get("close") is not None]
    clean.sort(key=lambda r: str(r.get("date", "")))
    if len(clean) <= slow + 2:
        raise ValueError(f"至少需要 {slow + 3} 条有效行情")

    closes = [float(r["close"]) for r in clean]
    opens = [float(r["open"]) for r in clean]
    equity = 1.0
    peak = 1.0
    position = 0.0
    entry_price = None
    signal_warmup = max(slow, vol_window, momentum_window)
    evaluation_start = signal_warmup
    if evaluation_start_date is not None:
        evaluation_start = next(
            (i for i, row in enumerate(clean)
             if str(row.get("date", "")) >= evaluation_start_date),
            len(clean),
        )
        if evaluation_start >= len(clean) - 1:
            raise ValueError("evaluation_start_date 之后没有足够行情")
    equity_curve = [{"date": clean[evaluation_start].get("date"), "equity": equity}]
    daily_returns: list[float] = []
    trades: list[dict[str, Any]] = []
    total_cost = 0.0

    for i in range(evaluation_start, len(clean) - 1):
        ma_fast = sum(closes[i-fast+1:i+1]) / fast
        ma_slow = sum(closes[i-slow+1:i+1]) / slow
        rets = [closes[j] / closes[j-1] - 1.0 for j in range(i-vol_window+1, i+1)]
        vol = math.sqrt(sum((x - sum(rets)/len(rets))**2 for x in rets) / max(1, len(rets)-1)) * math.sqrt(252)
        weight = min(max_weight, target_vol / vol) if vol > 1e-9 else max_weight
        signal = ma_fast > ma_slow and closes[i] > ma_slow
        if momentum_window:
            momentum = closes[i] / closes[i-momentum_window] - 1.0
            signal = signal and momentum > momentum_threshold
        if entry_price is not None and closes[i] <= entry_price * (1 - stop_loss):
            signal = False
        desired = weight if signal else 0.0
        fill = opens[i + 1]
        if desired != position:
            turnover = abs(desired - position)
            cost = turnover * fee_bp / 10000.0
            total_cost += cost
            equity *= 1.0 - cost
            trades.append({"date": clean[i+1].get("date"), "action": "buy" if desired > position else "sell", "weight": round(desired, 6), "price": fill, "cost": cost})
            if desired > position:
                entry_price = fill
            elif desired == 0:
                entry_price = None
            position = desired
        next_close = closes[i + 1]
        asset_return = next_close / fill - 1.0 if fill else 0.0
        day_ret = position * asset_return - (abs(desired - (trades[-1]["weight"] if trades and trades[-1]["date"] == clean[i+1].get("date") else position)) * fee_bp / 10000.0 if False else 0.0)
        # Trading cost is charged at the fill above; the portfolio return starts after execution.
        equity *= 1.0 + day_ret
        daily_returns.append(day_ret)
        peak = max(peak, equity)
        equity_curve.append({"date": clean[i+1].get("date"), "equity": equity})

    if position > 0:
        fill = closes[-1]
        cost = position * fee_bp / 10000.0
        equity *= 1.0 - cost
        total_cost += cost
        trades.append({"date": clean[-1].get("date"), "action": "sell", "weight": 0.0, "price": fill, "cost": cost, "reason": "end_of_test"})

    n = len(daily_returns)
    mean = sum(daily_returns) / n if n else 0.0
    std = math.sqrt(sum((x - mean) ** 2 for x in daily_returns) / max(1, n - 1)) if n else 0.0
    sharpe = mean / std * math.sqrt(252) if std > 1e-12 else 0.0
    max_dd = 0.0
    running = 1.0
    high = 1.0
    for r in daily_returns:
        running *= 1 + r
        high = max(high, running)
        max_dd = min(max_dd, running / high - 1)
    benchmark = closes[-1] / closes[evaluation_start] - 1.0
    return {
        "strategy": (
            f"MA{fast}/MA{slow} trend + {target_vol:.0%} volatility target + "
            f"{stop_loss:.0%} stop loss"
            + (f" + {momentum_window}-day momentum > {momentum_threshold:.2%}" if momentum_window else "")
        ),
        "period": {"start": clean[evaluation_start].get("date"), "end": clean[-1].get("date")},
        "observations": n,
        "final_equity": round(equity, 6),
        "total_return_pct": round((equity - 1) * 100, 3),
        "benchmark_buy_hold_pct": round(benchmark * 100, 3),
        "annualized_sharpe": round(sharpe, 4),
        "max_drawdown_pct": round(max_dd * 100, 3),
        "trade_count": len(trades),
        "total_cost_pct": round(total_cost * 100, 4),
        "trades": trades,
        "equity_curve": equity_curve,
        "note": "研究回测；未模拟涨跌停、盘口冲击、ETF分红差异和真实成交队列。",
    }


def returns_from_curve(curve: list[dict[str, Any]]) -> list[float]:
    return [curve[i]["equity"] / curve[i-1]["equity"] - 1.0 for i in range(1, len(curve))]


def summary_stats(result: dict[str, Any]) -> dict[str, Any]:
    return {k: result[k] for k in ("period", "observations", "final_equity", "total_return_pct", "benchmark_buy_hold_pct", "annualized_sharpe", "max_drawdown_pct", "trade_count", "total_cost_pct")}
