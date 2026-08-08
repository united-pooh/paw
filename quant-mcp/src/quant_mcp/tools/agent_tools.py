"""多智能体模拟工具：第11课架构 + 一票否决对照实验。"""

from mcp.server.fastmcp import FastMCP

from ..lib import multi_agent


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def simulate_multi_agent(
        use_risk: bool = True,
        seed: int = 2026,
    ) -> dict:
        """运行 500 天多智能体交易系统模拟（第11课架构）。

        Agent 分工：Regime(状态识别) + Signal(信号) + Meta(投票仲裁) +
        Risk(一票否决, FinMem 式教训记忆) + Execution(滑点成本)。
        行情为合成数据：趋势牛→震荡→危机→震荡→趋势熊。

        Args:
            use_risk: True = 完整多智能体（含 Risk 一票否决）；False = 对照组
            seed: 行情随机种子

        Returns:
            dict: 绩效指标（总收益/夏普/回撤）、风控否决事件、Regime 占比
        """
        return multi_agent.simulate(use_risk=use_risk, seed=seed)
