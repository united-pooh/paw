"""日频趋势回测工具。"""

from mcp.server.fastmcp import FastMCP

from ..lib import backtest, datasource


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def backtest_etf(
        symbol: str,
        start_date: str | None = None,
        end_date: str | None = None,
        fast: int = 20,
        slow: int = 60,
        vol_window: int = 20,
        target_vol: float = 0.15,
        max_weight: float = 1.0,
        fee_bp: float = 5.0,
        stop_loss: float = 0.08,
        momentum_window: int = 0,
        momentum_threshold: float = 0.0,
        evaluation_start_date: str | None = None,
        adjust: str = "qfq",
        source: str = "auto",
    ) -> dict:
        """拉取 ETF 日线并执行无前视偏差的 MA 趋势回测。

        信号在收盘确认、次日开盘成交；包含波动率目标仓位、均线退出、动量过滤、
        止损和交易成本。仅用于研究与模拟，不连接真实下单。
        """
        rows, source_used = datasource.fetch_kline_with_source(
            symbol, period="daily", start_date=start_date, end_date=end_date,
            adjust=adjust, source=source,
        )
        result = backtest.backtest_trend(
            rows, fast=fast, slow=slow, vol_window=vol_window,
            target_vol=target_vol, max_weight=max_weight,
            fee_bp=fee_bp, stop_loss=stop_loss,
            momentum_window=momentum_window,
            momentum_threshold=momentum_threshold,
            evaluation_start_date=evaluation_start_date,
        )
        result["symbol"] = symbol
        result["source_used"] = source_used
        result["adjust"] = adjust
        return result
