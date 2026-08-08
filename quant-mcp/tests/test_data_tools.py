"""数据工具测试：mock akshare/baostock，验证标准化输出与自动回退。

运行：cd quant-mcp && PYTHONPATH=src python -m pytest tests/test_data_tools.py -v
"""

import json
import os
import sys
import types

import pytest

from quant_mcp.lib import datasource


def test_no_proxy_patch():
    """行情域名必须写入 no_proxy，避免被本地代理拦截。"""
    np_ = (os.environ.get("NO_PROXY") or "").lower()
    for d in (".eastmoney.com", ".sina.com.cn", ".tushare.pro", ".baostock.com"):
        assert d in np_, f"no_proxy 缺少 {d}"


# ---------------------------------------------------------------------------
# 假数据源
# ---------------------------------------------------------------------------

class FakeAK:
    """假 akshare：提供日线/分钟/实时快照/股票列表。"""

    def stock_zh_a_hist(self, **kwargs):
        import pandas as pd

        return pd.DataFrame(
            [
                {"日期": "2026-08-05", "开盘": 10.0, "最高": 10.5, "最低": 9.8,
                 "收盘": 10.2, "成交量": 100000, "成交额": 1e8, "涨跌幅": 2.0},
                {"日期": "2026-08-06", "开盘": 10.2, "最高": 10.8, "最低": 10.0,
                 "收盘": 10.6, "成交量": 120000, "成交额": 1.2e8, "涨跌幅": 3.9},
            ]
        )

    def stock_zh_a_hist_min_em(self, **kwargs):
        import pandas as pd

        return pd.DataFrame(
            [
                {"时间": "2026-08-06 10:30:00", "开盘": 10.5, "最高": 10.6,
                 "最低": 10.4, "收盘": 10.55, "成交量": 1000, "成交额": 1e6},
            ]
        )

    def stock_zh_a_spot_em(self):
        import pandas as pd

        return pd.DataFrame(
            [
                {"代码": "000001", "名称": "平安银行", "最新价": 10.6,
                 "涨跌幅": 3.9, "涨跌额": 0.4, "成交量": 120000,
                 "成交额": 1.2e8, "振幅": 9.8, "换手率": 1.2, "量比": 1.5,
                 "市盈率-动态": 5.0, "市净率": 0.6},
                {"代码": "600519", "名称": "贵州茅台", "最新价": 1500.0,
                 "涨跌幅": 1.0, "涨跌额": 15.0, "成交量": 30000,
                 "成交额": 4.5e9, "振幅": 2.0, "换手率": 0.3, "量比": 0.8,
                 "市盈率-动态": 25.0, "市净率": 8.0},
            ]
        )

    def stock_info_a_code_name(self):
        import pandas as pd

        return pd.DataFrame([{"code": "000001", "name": "平安银行"}])


class FakeBS:
    """假 baostock：日线数据（前复权），校验日期格式必须为 YYYY-MM-DD。"""

    seen_start = None

    def login(self):
        return types.SimpleNamespace(error_code="0", error_msg="")

    def logout(self):
        pass

    def query_history_k_data_plus(self, code, fields, **kwargs):
        FakeBS.seen_start = kwargs.get("start_date")
        assert kwargs["start_date"][4] == "-", "baostock 日期格式应为 YYYY-MM-DD"
        assert kwargs["end_date"][4] == "-", "baostock 日期格式应为 YYYY-MM-DD"
        assert code.startswith(("sh.", "sz.")), f"代码格式错误: {code}"

        class RS:
            error_code = "0"
            error_msg = ""

            def __init__(self):
                self._rows = [
                    ["2026-08-05", "10.0", "10.5", "9.8", "10.2", "100000", "1e8", "2.0"],
                    ["2026-08-06", "10.2", "10.8", "10.0", "10.6", "120000", "1.2e8", "3.9"],
                ]
                self._i = -1

            def next(self):
                self._i += 1
                return self._i < len(self._rows)

            def get_row_data(self):
                return self._rows[self._i]

        return RS()


@pytest.fixture()
def fake_ak(monkeypatch):
    monkeypatch.setitem(sys.modules, "akshare", FakeAK())


@pytest.fixture()
def fake_bs(monkeypatch):
    monkeypatch.setitem(sys.modules, "baostock", FakeBS())


# ---------------------------------------------------------------------------
# fetch_kline
# ---------------------------------------------------------------------------


def test_fetch_kline_akshare_standardized(fake_ak):
    rows = datasource.fetch_kline("000001", source="akshare")
    assert len(rows) == 2
    r = rows[-1]
    assert r["date"] == "2026-08-06"
    assert r["open"] == 10.2
    assert r["close"] == 10.6
    assert r["pct_chg"] == 3.9
    assert set(r) >= {"date", "open", "high", "low", "close", "volume", "amount"}


def test_fetch_kline_minute(fake_ak):
    rows = datasource.fetch_kline("000001", period="5", source="akshare")
    assert len(rows) == 1
    assert rows[0]["close"] == 10.55


def test_fetch_kline_auto_fallback_ak_to_bs(fake_bs, monkeypatch):
    # akshare 缺失/失败 → 自动回退 baostock
    monkeypatch.delitem(sys.modules, "akshare", raising=False)
    rows = datasource.fetch_kline("000001", source="auto")
    assert len(rows) == 2
    assert rows[0]["date"] == "2026-08-05"


def test_fetch_kline_all_sources_fail(monkeypatch):
    class BrokenAK:
        def stock_zh_a_hist(self, **kwargs):
            raise RuntimeError("network down")

    class BrokenBS:
        def login(self):
            return types.SimpleNamespace(error_code="-1", error_msg="server down")

    monkeypatch.setitem(sys.modules, "akshare", BrokenAK())
    monkeypatch.setitem(sys.modules, "baostock", BrokenBS())
    with pytest.raises(RuntimeError, match="所有数据源均失败"):
        datasource.fetch_kline("000001", source="auto")


def test_fetch_kline_invalid_period():
    with pytest.raises(ValueError):
        datasource.fetch_kline("000001", period="weekly")


# ---------------------------------------------------------------------------
# fetch_realtime_quotes
# ---------------------------------------------------------------------------


def test_fetch_realtime_filter(fake_ak):
    rows = datasource.fetch_realtime_quotes(["000001"])
    assert len(rows) == 1
    assert rows[0]["symbol"] == "000001"
    assert rows[0]["name"] == "平安银行"
    assert rows[0]["pct_chg"] == 3.9
    assert rows[0]["volume_ratio"] == 1.5


def test_fetch_realtime_all(fake_ak):
    rows = datasource.fetch_realtime_quotes(None, limit=10)
    assert len(rows) == 2


# ---------------------------------------------------------------------------
# fetch_stock_list
# ---------------------------------------------------------------------------


def test_fetch_stock_list(fake_ak):
    stocks = datasource.fetch_stock_list()
    assert stocks[0]["symbol"] == "000001"
    assert stocks[0]["name"] == "平安银行"


# ---------------------------------------------------------------------------
# MCP 集成：工具注册与调用
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_data_tools_registered_and_callable(fake_ak):
    from mcp.shared.memory import create_connected_server_and_client_session
    from mcp.types import TextContent

    from quant_mcp.server import build

    server = build()
    async with create_connected_server_and_client_session(server) as session:
        tools = await session.list_tools()
        names = {t.name for t in tools.tools}
        assert {"fetch_kline", "fetch_realtime_quotes", "fetch_stock_list",
                "watch_snapshot"} <= names

        r = await session.call_tool("fetch_kline", {
            "symbol": "000001", "source": "akshare",
        })
        out = json.loads("\n".join(
            c.text for c in r.content if isinstance(c, TextContent)
        ))
        assert out["count"] == 2
        assert out["rows"][0]["close"] == 10.2
        assert out["source_used"] == "akshare"
        assert out["latest_date"] == "2026-08-06"

        r = await session.call_tool("fetch_realtime_quotes", {"symbols": ["600519"]})
        out = json.loads("\n".join(
            c.text for c in r.content if isinstance(c, TextContent)
        ))
        assert out["quotes"][0]["name"] == "贵州茅台"

        r = await session.call_tool("watch_snapshot", {"symbols": ["000001"]})
        out = json.loads("\n".join(
            c.text for c in r.content if isinstance(c, TextContent)
        ))
        assert out["trend"][0]["ma20"] is not None
        assert out["trend"][0]["bias_note"] in (
            "强势（价在 20 日线上方）", "弱势（价在 20 日线下方）", "震荡（贴近 20 日线）"
        )
