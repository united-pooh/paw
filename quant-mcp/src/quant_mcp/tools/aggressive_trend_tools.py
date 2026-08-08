"""进攻型牛市/震荡上行策略 MCP 工具。"""

from mcp.server.fastmcp import FastMCP

from ..lib import aggressive_trend


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def aggressive_trend_backtest(
        rows: list[dict],
        fast: int = 10,
        slow: int = 30,
        breakout_window: int = 20,
        momentum_window: int = 10,
        exit_buffer: float = 0.02,
        max_weight: float = 1.0,
        fee_bp: float = 5.0,
        trailing_stop: float = 0.15,
        evaluation_start_date: str | None = None,
    ) -> dict:
        """回测面向牛市满仓参与和震荡上行的进攻型趋势策略。

        价格位于慢均线上方且趋势向上时保持高仓位；突破近期高点或动量为正
        用于确认入场。采用宽幅回撤保护退出，牺牲部分回撤控制以提高牛市参与度。
        仅研究模拟，不连接真实下单。
        """
        return aggressive_trend.backtest_aggressive_trend(
            rows=rows,
            fast=fast,
            slow=slow,
            breakout_window=breakout_window,
            momentum_window=momentum_window,
            exit_buffer=exit_buffer,
            max_weight=max_weight,
            fee_bp=fee_bp,
            trailing_stop=trailing_stop,
            evaluation_start_date=evaluation_start_date,
        )
