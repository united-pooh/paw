"""五因子评分库与 MCP 工具测试。"""

import json

import pytest
from mcp.shared.memory import create_connected_server_and_client_session
from mcp.types import TextContent

from quant_mcp.lib.screening import score_rows
from quant_mcp.server import build


def _rows(n=80):
    return [
        {"date": f"2026-01-{(i % 28) + 1:02d}", "close": 10.0 + i * 0.05,
         "open": 10.0 + i * 0.05, "high": 10.1 + i * 0.05,
         "low": 9.9 + i * 0.05, "volume": 1000, "amount": 10000}
        for i in range(n)
    ]


def test_score_rows_requires_history():
    with pytest.raises(ValueError, match="至少需要 61 根"):
        score_rows(_rows(60), symbol="TEST")


def test_score_rows_has_five_factors():
    out = score_rows(_rows(), symbol="TEST")
    assert out["symbol"] == "TEST"
    assert set(out["factors"]) == {
        "momentum_60", "momentum_20", "ma20_deviation", "volatility", "ma60_trend"
    }
    assert 0 <= out["score"] <= 100
    assert out["metrics"]["trend_up"] is True


@pytest.mark.asyncio
async def test_screening_tools_registered():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        tools = await session.list_tools()
        names = {tool.name for tool in tools.tools}
        assert {"score_etf", "rank_etfs"} <= names


@pytest.mark.asyncio
async def test_score_etf_calls_datasource(monkeypatch):
    from quant_mcp.tools import screening_tools

    monkeypatch.setattr(
        screening_tools.datasource,
        "fetch_kline_with_source",
        lambda *args, **kwargs: (_rows(), "fixture"),
    )
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        result = await session.call_tool("score_etf", {"symbol": "159928"})
        text = "\n".join(c.text for c in result.content if isinstance(c, TextContent))
        out = json.loads(text)
        assert out["source_used"] == "fixture"
        assert out["symbol"] == "159928"
        assert out["observations"] == 80


@pytest.mark.asyncio
async def test_rank_etfs_keeps_single_symbol_errors(monkeypatch):
    from quant_mcp.tools import screening_tools

    def fake_fetch(symbol, **kwargs):
        if symbol == "BAD":
            raise RuntimeError("source unavailable")
        return _rows(), "fixture"

    monkeypatch.setattr(screening_tools.datasource, "fetch_kline_with_source", fake_fetch)
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        result = await session.call_tool("rank_etfs", {"symbols": ["GOOD", "BAD"]})
        text = "\n".join(c.text for c in result.content if isinstance(c, TextContent))
        out = json.loads(text)
        assert out["count"] == 1
        assert out["results"][0]["symbol"] == "GOOD"
        assert out["errors"][0]["symbol"] == "BAD"
