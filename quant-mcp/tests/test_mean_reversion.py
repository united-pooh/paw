"""均值回归策略单元测试。"""

from quant_mcp.lib.mean_reversion import backtest_mean_reversion


def _rows(closes):
    return [{"date": f"2025-01-{i+1:02d}", "open": x, "close": x} for i, x in enumerate(closes)]


def test_mean_reversion_returns_schema():
    closes = [100 + i * 0.1 for i in range(25)] + [102, 101, 99, 98, 99, 100, 101, 102, 103, 104]
    result = backtest_mean_reversion(_rows(closes), lookback=10, vol_window=10)
    assert "total_return_pct" in result
    assert result["observations"] > 0
    assert result["trade_count"] >= 0
