"""权益资产防守型仓位管理器：趋势确认、动量过滤与组合风险门。"""

from __future__ import annotations

import math
from typing import Any


def _clean(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out = [r for r in rows if r.get("close") is not None]
    out.sort(key=lambda r: str(r.get("date", "")))
    return out


def defensive_filter(
    asset_rows: dict[str, list[dict[str, Any]]],
    fast: int = 20,
    slow: int = 30,
    momentum_window: int = 15,
    momentum_threshold: float = 0.0,
    vol_window: int = 20,
    target_vol: float = 0.15,
    max_weight: float = 1.0,
    max_portfolio_weight: float = 1.0,
    min_history: int = 60,
) -> dict[str, Any]:
    """对多个权益资产生成防守型仓位建议。

    该函数只使用各资产最新收盘及其之前的数据，不连接交易通道。
    单资产权重先按目标波动率缩放，再按组合总风险门限等比例缩放。
    """
    if fast < 2 or fast >= slow:
        raise ValueError("需满足 2<=fast<slow")
    if momentum_window < 1 or vol_window < 2:
        raise ValueError("momentum_window>=1 且 vol_window>=2")
    if not 0 < target_vol <= 2 or not 0 < max_weight <= 1 or not 0 < max_portfolio_weight <= 1:
        raise ValueError("波动率和仓位参数超出范围")

    candidates: list[dict[str, Any]] = []
    errors: list[dict[str, str]] = []
    for symbol, raw in asset_rows.items():
        rows = _clean(raw)
        need = max(slow, momentum_window, vol_window) + 1
        if len(rows) < max(min_history, need):
            errors.append({"symbol": symbol, "error": f"历史数据不足，需要至少 {max(min_history, need)} 条"})
            continue
        closes = [float(r["close"]) for r in rows]
        i = len(closes) - 1
        ma_fast = sum(closes[i-fast+1:i+1]) / fast
        ma_slow = sum(closes[i-slow+1:i+1]) / slow
        momentum = closes[i] / closes[i-momentum_window] - 1.0
        rets = [closes[j] / closes[j-1] - 1.0 for j in range(i-vol_window+1, i+1)]
        mean = sum(rets) / len(rets)
        daily_vol = math.sqrt(sum((x - mean) ** 2 for x in rets) / max(1, len(rets)-1))
        annual_vol = daily_vol * math.sqrt(252)
        trend_ok = ma_fast > ma_slow and closes[i] > ma_slow
        momentum_ok = momentum > momentum_threshold
        signal = trend_ok and momentum_ok
        raw_weight = min(max_weight, target_vol / annual_vol) if annual_vol > 1e-9 else max_weight
        candidates.append({
            "symbol": symbol,
            "date": rows[-1].get("date"),
            "close": round(closes[i], 6),
            "ma_fast": round(ma_fast, 6),
            "ma_slow": round(ma_slow, 6),
            "momentum_pct": round(momentum * 100, 3),
            "annualized_vol_pct": round(annual_vol * 100, 3),
            "trend_ok": trend_ok,
            "momentum_ok": momentum_ok,
            "signal": signal,
            "raw_weight": round(raw_weight if signal else 0.0, 6),
        })

    active = [x for x in candidates if x["signal"]]
    total_raw = sum(x["raw_weight"] for x in active)
    scale = min(1.0, max_portfolio_weight / total_raw) if total_raw > 0 else 0.0
    for item in candidates:
        item["recommended_weight"] = round(item["raw_weight"] * scale, 6)
        item["action"] = "持有/配置" if item["recommended_weight"] > 0 else "现金/不配置"
    avg_momentum = sum(x["momentum_pct"] for x in active) / len(active) if active else 0.0
    if not active:
        regime = "risk_off"
        note = "没有资产同时满足趋势和动量条件，组合建议防守并持有现金。"
    elif len(active) >= max(1, len(candidates) / 2) and avg_momentum > 0:
        regime = "risk_on"
        note = "多数资产趋势与动量为正，可按风险预算配置，但仍受组合仓位上限约束。"
    else:
        regime = "mixed"
        note = "资产信号分化，建议仅配置通过过滤器的资产并保留现金缓冲。"
    return {
        "regime": regime,
        "assets": candidates,
        "active_count": len(active),
        "asset_count": len(candidates),
        "gross_weight": round(sum(x["recommended_weight"] for x in candidates), 6),
        "portfolio_weight_limit": max_portfolio_weight,
        "note": note,
        "errors": errors,
        "parameters": {
            "fast": fast, "slow": slow, "momentum_window": momentum_window,
            "momentum_threshold": momentum_threshold, "vol_window": vol_window,
            "target_vol": target_vol, "max_weight": max_weight,
            "max_portfolio_weight": max_portfolio_weight,
        },
    }


__all__ = ["defensive_filter"]
