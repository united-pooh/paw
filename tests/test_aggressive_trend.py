"""进攻型趋势策略测试。"""

from quant_mcp.lib.aggressive_trend import backtest_aggressive_trend


def _rows(closes):
    return [
        {"date": f"2025-01-{i + 1:02d}", "open": x, "close": x}
        for i, x in enumerate(closes)
    ]


def test_aggressive_trend_schema_and_positive_trend():
    closes = [100 + i * 0.8 for i in range(80)]
    result = backtest_aggressive_trend(_rows(closes))
    assert result["total_return_pct"] > 0
    assert result["benchmark_buy_hold_pct"] > 0
    assert result["trade_count"] >= 2
    assert result["max_drawdown_pct"] <= 0


def test_aggressive_trend_rejects_short_data():
    try:
        backtest_aggressive_trend(_rows([100 + i for i in range(20)]))
    except ValueError as exc:
        assert "至少需要" in str(exc)
    else:
        raise AssertionError("short data should be rejected")
