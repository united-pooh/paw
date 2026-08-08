"""Deflated Sharpe Ratio（Bailey & López de Prado 2014, JPM）。

所有夏普均为【非年化】（per-observation），年化 = ×sqrt(252)。
"""

import math

from .stats import GAMMA_EM, norm_cdf, norm_ppf

EULER = math.e


def expected_max_sharpe(mean_sr: float, var_sr: float, n_trials: int) -> float:
    """N 次独立试验后估计夏普的期望最大值（论文 Eq.6）。"""
    if n_trials <= 1:
        return mean_sr
    max_z = ((1.0 - GAMMA_EM) * norm_ppf(1.0 - 1.0 / n_trials)
             + GAMMA_EM * norm_ppf(1.0 - 1.0 / (n_trials * EULER)))
    return mean_sr + math.sqrt(var_sr) * max_z


def deflated_sharpe_ratio(est_sr: float, var_sr: float, n_trials: int,
                          t_len: int, skew: float = 0.0, kurt: float = 3.0) -> float:
    """DSR（论文 Eq.2）：观测夏普在修正选择偏差/非正态后的显著性概率。"""
    sr0 = expected_max_sharpe(0.0, var_sr, n_trials)
    denom = math.sqrt(1.0 - skew * est_sr + ((kurt - 1.0) / 4.0) * est_sr ** 2.0)
    z = ((est_sr - sr0) * math.sqrt(t_len - 1.0)) / denom
    return norm_cdf(z)


def min_sharpe_for_dsr(threshold: float, var_sr: float, n_trials: int,
                       t_len: int, skew: float = 0.0, kurt: float = 3.0) -> float:
    """二分法反推：要让 DSR >= threshold，观测夏普（非年化）至少要多高。"""
    lo, hi = 0.0, 10.0
    for _ in range(200):
        mid = (lo + hi) / 2.0
        if deflated_sharpe_ratio(mid, var_sr, n_trials, t_len, skew, kurt) >= threshold:
            hi = mid
        else:
            lo = mid
    return (lo + hi) / 2.0
