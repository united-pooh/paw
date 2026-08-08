"""五因子选股工具：拉取日线后计算可解释评分。"""

from mcp.server.fastmcp import FastMCP

from ..lib import datasource, screening


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def score_etf(
        symbol: str,
        start_date: str | None = None,
        end_date: str | None = None,
        adjust: str = "qfq",
        source: str = "auto",
    ) -> dict:
        """用五因子框架给一只 A 股 ETF/股票打分。

        五因子：60日动量、20日动量、MA20偏离、20日年化波动率、MA60趋势。
        返回实际数据源、最新日期、原始指标、各因子分和综合分；不把日线数据当实时行情。
        """
        rows, source_used = datasource.fetch_kline_with_source(
            symbol, period="daily", start_date=start_date, end_date=end_date,
            adjust=adjust, source=source,
        )
        result = screening.score_rows(rows, symbol=symbol)
        result["source_used"] = source_used
        result["adjust"] = adjust
        return result

    @mcp.tool()
    def rank_etfs(
        symbols: list[str],
        start_date: str | None = None,
        end_date: str | None = None,
        adjust: str = "qfq",
        source: str = "auto",
    ) -> dict:
        """批量按五因子框架排名 ETF/股票。

        单只数据失败不会阻塞其他标的；成功结果按综合分从高到低排列，并标明失败原因。
        """
        if not 1 <= len(symbols) <= 50:
            raise ValueError("symbols 数量需在 1~50 之间")
        results = []
        errors = []
        for symbol in symbols:
            try:
                rows, source_used = datasource.fetch_kline_with_source(
                    symbol, period="daily", start_date=start_date, end_date=end_date,
                    adjust=adjust, source=source,
                )
                result = screening.score_rows(rows, symbol=symbol)
                result["source_used"] = source_used
                result["adjust"] = adjust
                results.append(result)
            except Exception as exc:  # noqa: BLE001 — 批量筛选允许单只失败
                errors.append({"symbol": symbol, "error": str(exc)})
        results.sort(key=lambda item: item["score"], reverse=True)
        return {
            "count": len(results),
            "results": results,
            "errors": errors,
            "formula": "60日动量25% + 20日动量15% + MA20偏离20% + 波动率20% + MA60趋势20%",
            "note": "仅研究/模拟盘用途；实时交易前需交叉验证行情日期和数据源。",
        }
