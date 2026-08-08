"""权益防守型仓位管理器测试。"""

from quant_mcp.lib.defensive_filter import defensive_filter


def _rows(closes):
    return [{"date": f"2026-01-{i+1:02d}", "close": close} for i, close in enumerate(closes)]


def test_defensive_filter_risk_off_and_weight_cap():
    # 下跌资产不应配置；上涨资产满足条件但总仓位不得超过组合上限。
    up = [100 + i * 0.5 for i in range(80)]
    down = [100 - i * 0.5 for i in range(80)]
    result = defensive_filter(
        {"UP": _rows(up), "DOWN": _rows(down)},
        fast=20, slow=30, momentum_window=15, vol_window=20,
        max_portfolio_weight=0.6,
    )
    assert result["gross_weight"] <= 0.6
    assert next(x for x in result["assets"] if x["symbol"] == "DOWN")["signal"] is False
    assert next(x for x in result["assets"] if x["symbol"] == "UP")["recommended_weight"] > 0


def test_defensive_filter_empty_active_is_risk_off():
    down = [100 - i for i in range(80)]
    result = defensive_filter({"DOWN": _rows(down)})
    assert result["regime"] == "risk_off"
    assert result["active_count"] == 0
    assert result["gross_weight"] == 0.0
