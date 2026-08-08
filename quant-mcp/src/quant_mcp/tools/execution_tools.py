"""执行工具：Almgren-Chriss 最优执行（第19课 + AC 2000）。"""

from mcp.server.fastmcp import FastMCP

from ..lib import execution


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def calc_ac_execution(
        x_total: float = 100000.0,
        n_periods: int = 10,
        sigma: float = 0.02,
        gamma: float = 5e-7,
        eta: float = 1e-6,
        eps: float = 0.005,
        kt_values: list[float] | None = None,
        n_sims: int = 2000,
        seed: int = 42,
    ) -> dict:
        """Almgren-Chriss 最优执行成本-风险分析。

        回答："清仓 10 万股，怎么卖最划算？"——卖得快冲击大，卖得慢风险大，
        AC 框架在 E[cost] + λ·Var[cost] 下给出最优轨迹。

        Args:
            x_total: 需要卖出的总股数
            n_periods: 执行期数（如 10 期 = 10 天）
            sigma: 每期价格波动率（如 0.02 = 2%）
            gamma: 永久冲击系数（每股）
            eta: 临时冲击系数（每股/期）
            eps: 固定价差半宽（价格单位）
            kt_values: 扫描的 κT 值列表（κT 越大越激进；默认 [0.5, 2.0, 4.0]）
            n_sims: 蒙特卡洛模拟路径数
            seed: 随机种子

        Returns:
            dict: 各策略的期望成本/标准差/各期成交速率 + 解读
        """
        if n_periods < 1:
            raise ValueError("n_periods 必须 >= 1")
        if x_total <= 0:
            raise ValueError("x_total 必须 > 0")
        res = execution.compare_strategies(x_total, n_periods, sigma, gamma, eta,
                                           eps, 1.0, kt_values, n_sims, seed)
        results = res["results"]
        imm = next(r for r in results if r["strategy"] == "immediate")
        twap = next(r for r in results if r["strategy"] == "twap")
        best = min(results, key=lambda r: r["expected_cost"] + r["std_cost"] ** 2)
        return {
            **res,
            "results": [
                {"strategy": r["strategy"],
                 "expected_cost": round(r["expected_cost"], 0),
                 "std_cost": round(r["std_cost"], 0),
                 "fill_rates_pct": [round(v / x_total * 100, 1) for v in r["fill_rates"]]}
                for r in results
            ],
            "takeaways": {
                "immediate_vs_twap": (f"立即卖出成本 {imm['expected_cost']:.0f} > TWAP "
                                      f"{twap['expected_cost']:.0f}，但风险 "
                                      f"{imm['std_cost']:.0f} < {twap['std_cost']:.0f}"),
                "best_by_E_plus_var": best["strategy"],
                "lesson": "显示价 ≠ 成交价：把行情价当成交价 = 训练幻觉（第19课）",
            },
        }
