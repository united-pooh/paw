"""数据工具：东方财富 / akshare / baostock / tushare 统一行情数据入口（自动回退）。"""

from mcp.server.fastmcp import FastMCP

from ..lib import datasource


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def fetch_kline(
        symbol: str,
        period: str = "daily",
        start_date: str | None = None,
        end_date: str | None = None,
        adjust: str = "qfq",
        source: str = "auto",
    ) -> dict:
        """获取 A 股 K 线行情（东方财富/akshare/baostock/tushare 自动回退）。

        回答"拉某只股票的日线/分钟线数据"。返回标准化字段的列表。

        Args:
            symbol: 股票代码，6 位数字或带后缀（000001 / 000001.SZ / 600000.SH）
            period: 周期，daily 或 1/5/15/30/60（分钟线）
            start_date: 开始日期 YYYYMMDD，默认最近约 250 个交易日
            end_date: 结束日期 YYYYMMDD，默认今天
            adjust: 复权方式，none/qfq（前复权，默认）/hfq（后复权）
            source: 数据源，auto（默认，东方财富→tushare→akshare→baostock 回退）
                    /eastmoney/akshare/baostock/tushare

        Returns:
            dict: {"symbol", "period", "rows": [{"date","open","high","low",
                  "close","volume","amount","pct_chg"}, ...], "source_used"}
        """
        if source not in ("auto", "eastmoney", "akshare", "baostock", "tushare"):
            raise ValueError(f"source 必须是 auto/eastmoney/akshare/baostock/tushare，收到 {source}")
        rows, source_used = datasource.fetch_kline_with_source(
            symbol, period=period, start_date=start_date,
            end_date=end_date, adjust=adjust, source=source,
        )
        return {
            "symbol": symbol,
            "period": period,
            "adjust": adjust,
            "count": len(rows),
            "rows": rows,
            "latest_date": rows[-1]["date"] if rows else None,
            "source_used": source_used,
            "note": "数据来自免费源，仅研究用；关键结论请跨 ≥2 个源交叉验证",
        }

    @mcp.tool()
    def fetch_realtime_quotes(
        symbols: list[str] | None = None,
        limit: int = 50,
    ) -> dict:
        """获取 A 股实时行情快照（盯盘用）。

        回答"现在大盘/自选股什么情况"。基于 akshare 东方财富全市场快照。

        Args:
            symbols: 代码列表（如 ["000001", "600519"]）；None = 全市场
            limit: 返回条数上限，默认 50，最大 500

        Returns:
            dict: {"count", "quotes": [{"symbol","name","price","pct_chg",
                  "change","volume","amount","amplitude","turnover",
                  "volume_ratio","pe","pb"}, ...]}
        """
        if symbols is not None and len(symbols) > 100:
            raise ValueError("单次最多查询 100 只股票")
        limit = max(1, min(limit, 500))
        quotes = datasource.fetch_realtime_quotes(symbols, limit=limit)
        return {"count": len(quotes), "quotes": quotes}

    @mcp.tool()
    def fetch_stock_list() -> dict:
        """获取 A 股全部上市股票列表（代码+名称，可带行业/上市日期）。

        Returns:
            dict: {"count", "stocks": [{"symbol","name",...}, ...]}
        """
        stocks = datasource.fetch_stock_list()
        return {"count": len(stocks), "stocks": stocks[:2000]}

    @mcp.tool()
    def watch_snapshot(symbols: list[str]) -> dict:
        """盯盘快照：自选股的实时行情 + 近期趋势摘要（收盘价相对 20 日均线位置）。

        回答"帮我盯一下这几只股票"。拉实时快照 + 各自日线算均线偏离度。

        Args:
            symbols: 自选股代码列表（1~20 只）

        Returns:
            dict: {"snapshot": [...], "trend": [{"symbol","close","ma20",
                  "dev_pct","bias_note"}, ...]}
        """
        if not 1 <= len(symbols) <= 20:
            raise ValueError("自选股数量需在 1~20 之间")
        quotes = datasource.fetch_realtime_quotes(symbols, limit=len(symbols))
        by_code = {q["symbol"]: q for q in quotes}

        trend = []
        for sym in symbols:
            code = sym.split(".")[0]
            q = by_code.get(code)
            try:
                rows = datasource.fetch_kline(sym, period="daily", adjust="qfq")
                closes = [r["close"] for r in rows[-20:]]
                ma20 = sum(closes) / len(closes) if closes else 0.0
                price = q["price"] if q else (closes[-1] if closes else 0.0)
                dev = (price - ma20) / ma20 * 100 if ma20 else 0.0
                note = (
                    "强势（价在 20 日线上方）" if dev > 2
                    else "弱势（价在 20 日线下方）" if dev < -2
                    else "震荡（贴近 20 日线）"
                )
                trend.append(
                    {"symbol": code, "name": q["name"] if q else "",
                     "price": price, "ma20": round(ma20, 3),
                     "dev_pct": round(dev, 2), "bias_note": note}
                )
            except Exception:  # noqa: BLE001 — 单只失败不阻塞整批
                trend.append(
                    {"symbol": code, "name": q["name"] if q else "",
                     "price": q["price"] if q else None, "ma20": None,
                     "dev_pct": None, "bias_note": "行情获取失败"}
                )
        return {
            "snapshot": quotes,
            "trend": trend,
            "note": "仅研究用，不是投资建议",
        }
