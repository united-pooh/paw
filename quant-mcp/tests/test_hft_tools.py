"""HFT 工具集成测试（hftbacktest 方法论转化）。

运行：cd quant-mcp && PYTHONPATH=src python -m pytest tests/ -v
"""

import json

import pytest
from mcp.shared.memory import create_connected_server_and_client_session
from mcp.types import TextContent

from quant_mcp.server import build


def _text(result) -> str:
    parts = []
    for c in result.content:
        if isinstance(c, TextContent):
            parts.append(c.text)
    return "\n".join(parts)


@pytest.fixture(scope="module")
def session_maker():
    return build


@pytest.mark.asyncio
async def test_list_hft_tools():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        tools = await session.list_tools()
        names = {t.name for t in tools.tools}
        assert {"calibrate_glft_mm", "compute_microstructure_signals",
                "simulate_latency_impact"} <= names


@pytest.mark.asyncio
async def test_calibrate_glft_mm():
    import numpy as np
    rng = np.random.default_rng(7)
    n = 12000
    arrival = (rng.exponential(3.0, n) + rng.random(n)).tolist()
    mid_chg = rng.normal(0.0, 0.35, n).tolist()

    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("calibrate_glft_mm", {
            "arrival_depths": arrival, "mid_price_chg": mid_chg,
            "position": 2.0,
        })
        out = json.loads(_text(r))
        assert out["A_per_sec"] > 0
        assert out["k_per_tick"] > 0
        assert out["volatility_tick_per_sqrt_sec"] > 0
        assert out["half_spread_tick"] > 0
        assert out["skew_tick"] > 0
        # 持仓 2 → reservation 应低于 mid（skew>0），bid 低于 ask
        assert out["bid_tick"] < out["ask_tick"]
        assert 0.0 <= out["hit_probability_pct"] <= 100.0
        assert out["reference"]["hit_probability_eth_example_pct"] == pytest.approx(1.86)
        # 负持仓 → 报价上移（库存 skew 反向）
        r2 = await session.call_tool("calibrate_glft_mm", {
            "arrival_depths": arrival, "mid_price_chg": mid_chg, "position": -2.0,
        })
        o2 = json.loads(_text(r2))
        assert o2["reservation_price_tick"] > out["reservation_price_tick"]


@pytest.mark.asyncio
async def test_calibrate_glft_mm_validation():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        # 空序列：MCP 层将工具内 ValueError 转为 isError 响应
        r = await session.call_tool("calibrate_glft_mm", {
            "arrival_depths": [], "mid_price_chg": [0.1],
        })
        assert r.isError


@pytest.mark.asyncio
async def test_compute_microstructure_signals():
    import numpy as np
    rng = np.random.default_rng(11)
    n_snap = 120
    bids, bq, asks, aq = [], [], [], []
    mid0 = 100.0
    for i in range(n_snap):
        bpx = [mid0 - 0.5 * j for j in range(1, 6)]
        apx = [mid0 + 0.5 * j for j in range(1, 6)]
        obi_drive = np.sin(i / 10.0)
        bqty = [max(10.0 + obi_drive * 3 + rng.random() * 2, 0.5) for _ in range(5)]
        aqty = [max(10.0 - obi_drive * 3 + rng.random() * 2, 0.5) for _ in range(5)]
        bids.append(bpx); bq.append(bqty); asks.append(apx); aq.append(aqty)
        mid0 += rng.normal(0, 0.1)

    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("compute_microstructure_signals", {
            "bid_prices": bids, "bid_qty": bq,
            "ask_prices": asks, "ask_qty": aq,
            "depth_range": 0.05, "std_window": 30, "ic_horizon": 10,
        })
        out = json.loads(_text(r))
        assert out["n_snapshots"] == n_snap
        assert len(out["obis"]) == n_snap
        assert len(out["vamps"]) == n_snap
        # VAMP 应落在 best_bid 与 best_ask 之间（价格合理性质）
        for i in range(n_snap):
            v = out["vamps"][i]
            if v is not None:
                assert bids[i][0] - 1e-6 <= v <= asks[i][0] + 1e-6, f"快照 {i} VAMP 越界"
        # 标准化 OBI 约一半窗口后非空，且均值为 0（z-score 性质）
        std_vals = [v for v in out["std_obi"] if v is not None]
        assert len(std_vals) > n_snap // 2
        assert abs(sum(std_vals) / len(std_vals)) < 0.5
        assert "ic" in out and "std_obi" in out["ic"]


@pytest.mark.asyncio
async def test_simulate_latency_impact():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("simulate_latency_impact", {
            "n_steps": 3000, "latency_steps": [0, 3, 10], "seed": 42,
        })
        out = json.loads(_text(r))
        conf = {c["latency_steps"]: c for c in out["configs"]}
        # 延迟越高成交越少、PnL 越差（官方定性结论：延迟决定生死）
        assert conf[0]["n_fills"] > conf[10]["n_fills"]
        assert conf[0]["pnl"] > conf[10]["pnl"]
        assert "延迟" in out["verdict"]
        assert out["reference"]["official_eth_example"][1]["sr"] == pytest.approx(1.54)


@pytest.mark.asyncio
async def test_hftbacktest_playbook_resource():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.read_resource("quant://notes/hftbacktest-playbook")
        text = "".join(str(c) for c in r.contents[0].text)
        assert "GLFT" in text and "half_spread" in text
        assert "返佣" in text and "延迟" in text
        assert "OBI" in text and "VAMP" in text
        assert len(text) > 500
