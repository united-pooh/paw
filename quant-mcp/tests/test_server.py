"""集成测试：InMemoryTransport 全链路验证 Tools / Resources / Prompts。

运行：cd quant-mcp && PYTHONPATH=src python -m pytest tests/ -v
"""

import json

import pytest
from mcp.shared.memory import create_connected_server_and_client_session
from mcp.types import TextContent

from quant_mcp.server import build


def _text(result) -> str:
    """提取 call_tool 结果的文本。"""
    parts = []
    for c in result.content:
        if isinstance(c, TextContent):
            parts.append(c.text)
    return "\n".join(parts)


@pytest.mark.asyncio
async def test_list_tools():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        tools = await session.list_tools()
        names = {t.name for t in tools.tools}
        assert {"calc_deflated_sharpe", "calc_min_sharpe", "calc_ac_execution",
                "detect_regime_hmm", "autopsy_strategy", "simulate_multi_agent",
                "defensive_equity_filter", "mean_reversion_backtest",
                "aggressive_trend_backtest"} <= names


@pytest.mark.asyncio
async def test_calc_deflated_sharpe():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        # 论文数值例子：N=100 → DSR≈0.8997（不通过）
        # 注意：论文例子的各试验夏普方差是 0.5/252，需显式传入（默认是 1/T 理论值）
        r = await session.call_tool("calc_deflated_sharpe", {
            "annualized_sharpe": 2.5, "n_trials": 100,
            "sample_days": 1250, "trials_sharpe_variance": 0.5 / 252,
            "skew": -3.0, "kurt": 10.0,
        })
        out = json.loads(_text(r))
        assert out["dsr"] == pytest.approx(0.8997, abs=0.002)
        assert out["pass_95pct"] is False

        # 论文：N=46 → DSR≈0.9505（通过）
        r = await session.call_tool("calc_deflated_sharpe", {
            "annualized_sharpe": 2.5, "n_trials": 46,
            "sample_days": 1250, "trials_sharpe_variance": 0.5 / 252,
            "skew": -3.0, "kurt": 10.0,
        })
        out = json.loads(_text(r))
        assert out["dsr"] >= 0.95


@pytest.mark.asyncio
async def test_calc_min_sharpe():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("calc_min_sharpe", {"n_trials": 100})
        out = json.loads(_text(r))
        # T=1250 且 var=1/T 口径：~1.88
        assert out["min_annualized_sharpe"] > 1.8
        # 演示4口径（var=1/252）：试 100 次要 3.3+ 才合格
        r = await session.call_tool("calc_min_sharpe", {"n_trials": 100, "sample_days": 252})
        out = json.loads(_text(r))
        assert out["min_annualized_sharpe"] > 3.0


@pytest.mark.asyncio
async def test_calc_ac_execution():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("calc_ac_execution", {
            "x_total": 100000, "n_periods": 10, "n_sims": 500,
        })
        out = json.loads(_text(r))
        results = {x["strategy"]: x for x in out["results"]}
        assert "immediate" in results and "twap" in results and "ac_kt_2.0" in results
        # 立即卖出成本应显著高于 TWAP
        assert results["immediate"]["expected_cost"] > results["twap"]["expected_cost"]
        # 立即卖出风险应显著低于 TWAP
        assert results["immediate"]["std_cost"] < results["twap"]["std_cost"]


@pytest.mark.asyncio
async def test_detect_regime_hmm():
    import numpy as np
    rng = np.random.default_rng(42)
    rets = np.concatenate([
        rng.normal(0.002, 0.008, 220),   # 趋势
        rng.normal(0.0, 0.005, 200),     # 震荡
        rng.normal(-0.012, 0.022, 80),   # 危机
    ]).tolist()
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("detect_regime_hmm", {"returns": rets, "n_states": 3})
        out = json.loads(_text(r))
        assert len(out["states"]) == len(rets) - 20
        assert set(out["state_params"][0]) == {"name", "mu_ret", "mu_vol", "vol"}
        assert len(out["transitions"]) == 3
        # 状态占比：危机应是最小的
        assert out["state_share"]["危机"] < out["state_share"]["震荡"]


@pytest.mark.asyncio
async def test_autopsy_strategy():
    import numpy as np
    rng = np.random.default_rng(7)
    rets = rng.normal(0.0008, 0.01, 756).tolist()
    rets[100] = -0.15   # 数据污染
    for i in range(600, 615):
        rets[i] = -0.02  # 危机段
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r = await session.call_tool("autopsy_strategy", {
            "returns": rets, "n_trials": 50, "slippage_bp": 5, "turnover": 0.5,
        })
        out = json.loads(_text(r))
        assert out["health_score"] < 0.5
        ids = {c["id"] for c in out["checks"] if c["status"] == "❌"}
        assert 1 in ids and 5 in ids   # 抓到数据污染与风控失效
        assert len(out["checks"]) == 12


@pytest.mark.asyncio
async def test_simulate_multi_agent():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        r1 = await session.call_tool("simulate_multi_agent", {"use_risk": True})
        r2 = await session.call_tool("simulate_multi_agent", {"use_risk": False})
        o1, o2 = json.loads(_text(r1)), json.loads(_text(r2))
        # 有风控应优于无风控（总收益/回撤）
        assert o1["total_return"] >= o2["total_return"]
        assert o1["max_drawdown"] <= o2["max_drawdown"]
        assert o1["vetoes"] > 0


@pytest.mark.asyncio
async def test_resources():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        for uri in ("quant://notes/papers-map", "quant://notes/death-modes",
                    "quant://notes/reading-path"):
            r = await session.read_resource(uri)
            text = "".join(str(c) for c in r.contents[0].text)
            assert len(text) > 200


@pytest.mark.asyncio
async def test_prompts():
    server = build()
    async with create_connected_server_and_client_session(server) as session:
        prompts = await session.list_prompts()
        names = {p.name for p in prompts.prompts}
        assert {"review_strategy", "read_papers"} <= names
