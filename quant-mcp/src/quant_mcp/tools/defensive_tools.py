"""权益资产防守型仓位管理与组合风险过滤工具。"""

from mcp.server.fastmcp import FastMCP

from ..lib import defensive_filter


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def defensive_equity_filter(
        asset_rows: dict[str, list[dict]],
        fast: int = 20,
        slow: int = 30,
        momentum_window: int = 15,
        momentum_threshold: float = 0.0,
        vol_window: int = 20,
        target_vol: float = 0.15,
        max_weight: float = 1.0,
        max_portfolio_weight: float = 1.0,
        min_history: int = 60,
    ) -> dict:
        """权益资产防守型仓位管理器/组合风险过滤器。

        对每个资产使用 MA20/MA30 趋势、15日动量和波动率目标仓位生成
        recommended_weight。信号不满足时建议持有现金；通过资产之间的总仓位
        上限控制组合风险。仅用于研究和模拟，不连接真实下单。

        asset_rows 的格式为 {"symbol": [{"date": "YYYY-MM-DD", "close": 1.0}, ...]}。
        至少提供 slow、momentum_window、vol_window 所需的历史数据。
        """
        return defensive_filter.defensive_filter(
            asset_rows=asset_rows,
            fast=fast,
            slow=slow,
            momentum_window=momentum_window,
            momentum_threshold=momentum_threshold,
            vol_window=vol_window,
            target_vol=target_vol,
            max_weight=max_weight,
            max_portfolio_weight=max_portfolio_weight,
            min_history=min_history,
        )
