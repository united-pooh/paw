"""反过拟合工具：Deflated Sharpe Ratio 与最小合格夏普反推。"""

import math

from mcp.server.fastmcp import FastMCP

from ..lib import deflated_sharpe as ds

ANNUAL = 252


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def calc_deflated_sharpe(
        annualized_sharpe: float,
        n_trials: int = 1,
        sample_days: int = 1250,
        trials_sharpe_variance: float | None = None,
        skew: float = 0.0,
        kurt: float = 3.0,
    ) -> dict:
        """计算 Deflated Sharpe Ratio（Bailey & López de Prado 2014）。

        回答核心问题："这个回测夏普是运气吗？" DSR >= 0.95 才算通过 95% 检验。

        Args:
            annualized_sharpe: 回测得到的年化夏普（观测值）
            n_trials: 回测阶段试过的参数/策略组合总数（选择偏差修正的关键）
            sample_days: 回测样本长度（交易日数）
            trials_sharpe_variance: 各次试验夏普的方差（默认 1/T 理论值）
            skew: 收益偏度（默认 0 正态）
            kurt: 收益峰度（默认 3 正态）

        Returns:
            dict: dsr、基准夏普 E[max SR]、是否通过 95% 检验、最小合格夏普
        """
        if n_trials < 1:
            raise ValueError("n_trials 必须 >= 1")
        if sample_days < 30:
            raise ValueError("sample_days 过短（<30），DSR 无意义")
        est_sr = annualized_sharpe / math.sqrt(ANNUAL)
        var_sr = trials_sharpe_variance if trials_sharpe_variance is not None else 1.0 / sample_days
        dsr = ds.deflated_sharpe_ratio(est_sr, var_sr, n_trials, sample_days, skew, kurt)
        emax = ds.expected_max_sharpe(0.0, var_sr, n_trials) * math.sqrt(ANNUAL)
        need = ds.min_sharpe_for_dsr(0.95, var_sr, n_trials, sample_days, skew, kurt)
        return {
            "dsr": round(dsr, 4),
            "expected_max_sharpe_annualized": round(emax, 3),
            "pass_95pct": dsr >= 0.95,
            "min_annualized_sharpe_for_95pct": round(need * math.sqrt(ANNUAL), 3),
            "note": ("试验次数越多、样本越短，需要的夏普越高——"
                     "不披露试验次数的回测报告 = 耍流氓" if n_trials > 1 else
                     "只试过一次：选择偏差为零，DSR 退化为常规显著性检验"),
        }

    @mcp.tool()
    def calc_min_sharpe(
        n_trials: int,
        sample_days: int = 1250,
        skew: float = 0.0,
        kurt: float = 3.0,
    ) -> dict:
        """反推最小合格夏普：给定试验次数 N 与样本长度，年化夏普至少要多高才能通过 95% 检验。

        用于"试了 100 个参数，回测夏普 3.0 够不够"这类问题。

        Args:
            n_trials: 试过的参数/策略组合总数
            sample_days: 回测样本长度（交易日数）
            skew: 收益偏度
            kurt: 收益峰度

        Returns:
            dict: 最小合格年化夏普、对应非年化值、E[max SR]
        """
        var_sr = 1.0 / sample_days
        need = ds.min_sharpe_for_dsr(0.95, var_sr, n_trials, sample_days, skew, kurt)
        emax = ds.expected_max_sharpe(0.0, var_sr, n_trials)
        return {
            "n_trials": n_trials,
            "sample_days": sample_days,
            "min_annualized_sharpe": round(need * math.sqrt(ANNUAL), 3),
            "expected_max_sharpe_annualized_by_luck": round(emax * math.sqrt(ANNUAL), 3),
        }
