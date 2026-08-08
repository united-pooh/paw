"""Regime 工具：手写高斯 HMM 市场状态识别（第12课）。"""

from mcp.server.fastmcp import FastMCP

from ..lib import hmm


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def detect_regime_hmm(
        returns: list[float],
        n_states: int = 3,
        lookback: int = 20,
    ) -> dict:
        """用高斯 HMM（Baum-Welch 手写实现）识别市场状态。

        输入日收益率序列，输出每个时刻的隐藏状态（震荡/趋势/危机）与
        最新状态概率、转移矩阵、各状态参数。适合给策略做 Regime 路由。

        Args:
            returns: 日收益率序列（至少 100 个观测）
            n_states: 隐藏状态数（默认 3：震荡/趋势/危机）
            lookback: 滚动波动率特征窗口（默认 20 天）

        Returns:
            dict: 状态序列（与输入对齐，前 lookback 天为预热）、
                  状态概率、转移矩阵、状态参数、状态占比
        """
        if len(returns) < 100:
            raise ValueError("returns 至少需要 100 个观测")
        if n_states < 2 or n_states > 6:
            raise ValueError("n_states 建议在 2~6 之间")
        return hmm.fit_regime_model(returns, n_states, lookback)
