"""模拟盘引擎测试：T+1 / 整手 / 费用 / 挂单撮合 / 日终结算。

运行：cd quant-mcp && PYTHONPATH=src python -m pytest tests/test_paper_sim.py -v
"""

import json

import pytest

from quant_mcp.lib import paper_sim

DB = "/tmp/paper_sim_test.db"


@pytest.fixture(autouse=True)
def clean_db():
    import os

    for suffix in ("", "-wal", "-shm"):
        try:
            os.remove(DB + suffix)
        except FileNotFoundError:
            pass
    yield


def _new_account(cash=100_000.0) -> str:
    return paper_sim.create_account("测试", cash, db_path=DB)["account_id"]


def test_create_account_and_summary():
    acc = _new_account()
    s = paper_sim.get_account_summary(acc, db_path=DB)
    assert s["cash"] == 100_000.0
    assert s["total_equity"] == 100_000.0
    assert s["positions"] == []


def test_buy_fills_and_charges_commission():
    acc = _new_account()
    r = paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0,
                               db_path=DB)
    assert r["status"] == "filled"
    # 佣金 = max(10000 * 0.00025, 5) = 5
    s = paper_sim.get_account_summary(acc, db_path=DB)
    assert s["cash"] == pytest.approx(100_000 - 10_000 - 5, abs=0.01)
    assert s["positions"][0]["qty"] == 1000
    assert s["positions"][0]["avg_cost"] == 10.0


def test_t_plus_1_blocks_same_day_sell():
    acc = _new_account()
    paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0, db_path=DB)
    r = paper_sim.submit_order(acc, "000001", "sell", 500, price=11.0,
                               trade_date="2026-08-07", db_path=DB)
    # 同一天买入不可卖 → 废单
    assert r["status"] == "rejected"
    assert "不可卖" in r["reason"]


def test_t_plus_1_unfreezes_next_day():
    acc = _new_account()
    paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0,
                           trade_date="2026-08-06", db_path=DB)
    r = paper_sim.submit_order(acc, "000001", "sell", 1000, price=11.0,
                               trade_date="2026-08-07", db_path=DB)
    assert r["status"] == "filled"
    s = paper_sim.get_account_summary(acc, db_path=DB)
    assert s["positions"] == []


def test_insufficient_cash_rejected():
    acc = _new_account(cash=10_000.0)
    r = paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0,
                               db_path=DB)
    assert r["status"] == "rejected"
    assert "资金不足" in r["reason"]


def test_insufficient_position_rejected():
    acc = _new_account()
    r = paper_sim.submit_order(acc, "000001", "sell", 100, price=10.0,
                               db_path=DB)
    assert r["status"] == "rejected"
    assert "可卖持仓不足" in r["reason"]


def test_lot_size_100():
    acc = _new_account()
    r = paper_sim.submit_order(acc, "000001", "buy", 150, price=10.0,
                               db_path=DB)
    assert r["status"] == "rejected"
    assert "100 股整数倍" in r["reason"]


def test_sell_charges_stamp_tax():
    acc = _new_account()
    paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0,
                           trade_date="2026-08-06", db_path=DB)
    r = paper_sim.submit_order(acc, "000001", "sell", 1000, price=11.0,
                               trade_date="2026-08-07", db_path=DB)
    # 卖出额 11000：佣金 5（万2.5 最低5）+ 印花税 5.5（0.05%）
    assert r["fee"] == pytest.approx(5.0 + 11000 * 0.0005, abs=0.01)


def test_pending_order_and_fill():
    acc = _new_account()
    r = paper_sim.submit_order(acc, "000001", "buy", 1000, price=9.5,
                               auto_fill=False, db_path=DB)
    assert r["status"] == "pending"
    # 最新价 10 > 委托价 9.5：买入不触发
    res = paper_sim.fill_pending_orders(acc, {"000001": 10.0}, db_path=DB)
    assert len(res["filled"]) == 0
    # 最新价 9.4 ≤ 委托价：成交
    res = paper_sim.fill_pending_orders(acc, {"000001": 9.4}, db_path=DB)
    assert len(res["filled"]) == 1
    assert res["filled"][0]["price"] == 9.5


def test_cancel_pending():
    acc = _new_account()
    r = paper_sim.submit_order(acc, "000001", "buy", 1000, price=9.5,
                               auto_fill=False, db_path=DB)
    res = paper_sim.cancel_order(r["id"], db_path=DB)
    assert res["status"] == "cancelled"


def test_mark_to_market_and_daily_settle():
    acc = _new_account()
    paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0,
                           trade_date="2026-08-06", db_path=DB)
    s = paper_sim.daily_settle(acc, price_map={"000001": 12.0},
                               trade_date="2026-08-06", db_path=DB)
    assert s["market_value"] == 12_000.0
    assert s["unrealized_pnl"] == pytest.approx(2_000.0)
    assert s["total_equity"] == pytest.approx(100_000 - 10_005 + 12_000)
    # 净值曲线
    curve = paper_sim.get_equity_curve(acc, db_path=DB)
    assert len(curve["points"]) == 1
    assert curve["points"][0]["total_equity"] == s["total_equity"]


def test_close_account():
    acc = _new_account()
    r = paper_sim.close_account(acc, db_path=DB)
    assert r["closed"] is True
    with pytest.raises(ValueError):
        paper_sim.get_account_summary(acc, db_path=DB)


def test_market_order_with_price():
    acc = _new_account()
    r = paper_sim.submit_order(acc, "000001", "buy", 1000, order_type="market",
                               price=10.0, db_path=DB)
    assert r["status"] == "filled"
    assert r["filled_price"] == 10.0


def test_orders_query_by_status():
    acc = _new_account()
    paper_sim.submit_order(acc, "000001", "buy", 1000, price=10.0, db_path=DB)
    paper_sim.submit_order(acc, "000002", "buy", 100, price=20.0, db_path=DB)
    filled = paper_sim.get_orders(acc, status="filled", db_path=DB)
    assert len(filled["orders"]) == 2
    all_orders = paper_sim.get_orders(acc, db_path=DB)
    assert len(all_orders["orders"]) == 2


@pytest.mark.asyncio
async def test_paper_tools_registered():
    from mcp.shared.memory import create_connected_server_and_client_session

    from quant_mcp.server import build

    server = build()
    async with create_connected_server_and_client_session(server) as session:
        tools = await session.list_tools()
        names = {t.name for t in tools.tools}
        assert {"create_paper_account", "paper_submit_order",
                "paper_daily_settle", "paper_account_summary",
                "paper_fill_pending"} <= names
        # 全链路：建账户 → 买入 → 结算
        r = await session.call_tool("create_paper_account", {"name": "e2e"})
        acc = json.loads(_text(r))["account_id"]
        r = await session.call_tool("paper_submit_order", {
            "account_id": acc, "symbol": "600519", "direction": "buy",
            "qty": 100, "price": 1500.0, "trade_date": "2026-08-06",
        })
        assert json.loads(_text(r))["status"] == "filled"
        r = await session.call_tool("paper_daily_settle", {
            "account_id": acc, "price_map": {"600519": 1550.0},
            "trade_date": "2026-08-06",
        })
        out = json.loads(_text(r))
        assert out["unrealized_pnl"] == pytest.approx(5000.0, abs=1.0)


def _text(result) -> str:
    from mcp.types import TextContent

    return "\n".join(
        c.text for c in result.content if isinstance(c, TextContent)
    )
