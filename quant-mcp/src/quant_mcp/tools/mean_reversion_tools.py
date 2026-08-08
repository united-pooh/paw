"""震荡/高波动策略 MCP 工具。"""

from mcp.server.fastmcp import FastMCP

from ..lib import mean_reversion


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def mean_reversion_backtest(
        rows: list[dict],
        lookback: int = 20,
        entry_z: float = -1.5,
        exit_z: float = -0.1,
        max_hold_days: int = 10,
        vol_window: int = 20,
        max_annual_vol: float = 0.65,
        target_vol: float = 0.15,
        max_weight: float = 0.5,
        fee_bp: float = 5.0,
        evaluation_start_date: str | None = None,
    ) -> dict:
        """回测震荡/高波动适配的 long-only 均值回归策略。

        价格显著偏离滚动均值、出现反弹确认且波动率未超过上限时，次日开盘入场；
        回到均值附近、超时或波动率过高时退出。仅研究模拟，不连接真实下单。
        """
        return mean_reversion.backtest_mean_reversion(
            rows=rows, lookback=lookback, entry_z=entry_z, exit_z=exit_z,
            max_hold_days=max_hold_days, vol_window=vol_window,
            max_annual_vol=max_annual_vol, target_vol=target_vol,
            max_weight=max_weight, fee_bp=fee_bp,
            evaluation_start_date=evaluation_start_date,
        )
