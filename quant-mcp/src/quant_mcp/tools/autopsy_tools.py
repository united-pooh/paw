"""体检工具：12 种死亡方式自动体检（附录B）。"""

from mcp.server.fastmcp import FastMCP

from ..lib import autopsy


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def autopsy_strategy(
        returns: list[float],
        n_trials: int = 1,
        slippage_bp: float = 1.0,
        turnover: float = 0.1,
        leverage: float = 1.0,
        multi_asset: list[list[float]] | None = None,
    ) -> dict:
        """对策略收益序列做"12 种死亡方式"完整体检（附录B）。

        覆盖：数据污染、过拟合(DSR)、Regime 漂移、执行失真、风控失效、
        流动性(N/A)、相关性飙升、杠杆爆仓、人为干预(N/A)、系统故障(N/A)、
        监管变化(N/A)、对手盘适应。

        Args:
            returns: 策略日收益率序列（至少 250 个观测）
            n_trials: 回测阶段试过的参数/策略组合数
            slippage_bp: 每笔滑点（基点）
            turnover: 日均换手率（0~1）
            leverage: 杠杆倍数
            multi_asset: 可选多资产日收益矩阵（每列一个资产），用于相关性检查

        Returns:
            dict: 12 项检查明细 + 健康度评分 + 病危项清单
        """
        if len(returns) < 250:
            raise ValueError("returns 至少需要 250 个观测（1 年日频）")
        return autopsy.autopsy(returns, n_trials, slippage_bp, turnover,
                               leverage, multi_asset)
