"""基础统计工具：正态分布函数、夏普、偏度/峰度（无 scipy 依赖）。"""

import math

import numpy as np

GAMMA_EM = 0.5772156649015328606  # Euler-Mascheroni 常数
ANNUAL = 252


def norm_cdf(z: float) -> float:
    """标准正态 CDF：Phi(z) = 0.5 * (1 + erf(z / sqrt(2)))."""
    return 0.5 * (1.0 + math.erf(z / math.sqrt(2.0)))


def norm_ppf(p: float) -> float:
    """标准正态分位数 Phi^{-1}(p)，Acklam 算法，精度 ~1e-9."""
    if not 0.0 < p < 1.0:
        raise ValueError(f"p 必须在 (0,1) 内，收到 {p}")
    a = [-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
         1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00]
    b = [-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
         6.680131188771972e+01, -1.328068155288572e+01]
    c = [-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
         -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00]
    d = [7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
         3.754408661907416e+00]
    plow = 0.02425
    if p < plow:
        q = math.sqrt(-2.0 * math.log(p))
        num = (((((c[0] * q + c[1]) * q + c[2]) * q + c[3]) * q + c[4]) * q + c[5])
        den = ((((d[0] * q + d[1]) * q + d[2]) * q + d[3]) * q + 1.0)
        return num / den
    if p <= 1.0 - plow:
        q = p - 0.5
        r = q * q
        num = (((((a[0] * r + a[1]) * r + a[2]) * r + a[3]) * r + a[4]) * r + a[5]) * q
        den = (((((b[0] * r + b[1]) * r + b[2]) * r + b[3]) * r + b[4]) * r + 1.0)
        return num / den
    q = math.sqrt(-2.0 * math.log(1.0 - p))
    num = (((((c[0] * q + c[1]) * q + c[2]) * q + c[3]) * q + c[4]) * q + c[5])
    den = ((((d[0] * q + d[1]) * q + d[2]) * q + d[3]) * q + 1.0)
    return -num / den


def sharpe(returns: np.ndarray) -> float:
    """非年化夏普：mean / std (ddof=1)。"""
    sd = returns.std(ddof=1)
    return float(returns.mean() / sd) if sd > 0 else 0.0


def annualized_sharpe(returns: np.ndarray) -> float:
    """年化夏普（252 交易日）。"""
    return sharpe(returns) * math.sqrt(ANNUAL)


def skew_kurt(returns: np.ndarray) -> tuple[float, float]:
    """样本偏度 g3 与峰度 g4（与 López de Prado 论文定义一致）。"""
    n = len(returns)
    m2 = float(((returns - returns.mean()) ** 2).sum()) / n
    m3 = float(((returns - returns.mean()) ** 3).sum()) / n
    m4 = float(((returns - returns.mean()) ** 4).sum()) / n
    g3 = m3 / m2 ** 1.5 if m2 > 0 else 0.0
    g4 = m4 / m2 ** 2.0 if m2 > 0 else 0.0
    return g3, g4


def max_drawdown(returns: np.ndarray) -> float:
    """最大回撤（0~1）。"""
    eq = np.cumprod(1.0 + returns)
    peak = np.maximum.accumulate(eq)
    return float((1.0 - eq / peak).max())
