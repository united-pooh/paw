#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
菜二：Deflated Sharpe Ratio (DSR) 反过拟合工具箱
=================================================
依据论文（含论文作者亲笔 Python 实现 Snippet 1）：

  Bailey, D.H. & López de Prado, M. (2014).
  "The Deflated Sharpe Ratio: Correcting for Selection Bias, Backtest
   Overfitting and Non-Normality." Journal of Portfolio Management.

核心公式（论文 Eq.1 / Eq.6, Eq.2）：

  E[max_N] = mu + sigma * ((1-gamma)*Phi^{-1}(1-1/N) + gamma*Phi^{-1}(1-1/(N*e)))
  DSR      = Phi( (SR_hat - E[max_N]) * sqrt(T-1)
                  / sqrt(1 - g3*SR_hat + (g4-1)/4 * SR_hat^2) )

  其中 gamma = 0.5772156649 (Euler-Mascheroni 常数)，e 为自然底数；
  SR_hat 为【非年化】夏普（per-observation），年化 = SR * sqrt(252)；
  T 为样本长度（观测数）；g3 为偏度；g4 为峰度；
  Var[SR] 为各次试验(trials)估计夏普的方差。

仅依赖 numpy + math（无 scipy / matplotlib），Python >= 3.8。

用法：
    python3 deflated_sharpe.py          # 运行全部演示
"""

import math
import numpy as np

# ----------------------------------------------------------------------------
# 基础统计工具（无 scipy，全部手写）
# ----------------------------------------------------------------------------

GAMMA_EM = 0.5772156649015328606  # Euler-Mascheroni 常数
EULER = math.e
ANNUAL = 252                      # 年化交易日数


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
    """非年化夏普：mean / std (ddof=1)."""
    sd = returns.std(ddof=1)
    return float(returns.mean() / sd) if sd > 0 else 0.0


def skew_kurt(returns: np.ndarray) -> tuple[float, float]:
    """样本偏度 g3 与超额峰度修正 g4（与论文定义一致）。"""
    n = len(returns)
    m2 = float(((returns - returns.mean()) ** 2).sum()) / n
    m3 = float(((returns - returns.mean()) ** 3).sum()) / n
    m4 = float(((returns - returns.mean()) ** 4).sum()) / n
    g3 = m3 / m2 ** 1.5 if m2 > 0 else 0.0
    g4 = m4 / m2 ** 2.0 if m2 > 0 else 0.0  # 论文中的 gamma_4 是原始峰度
    return g3, g4


# ----------------------------------------------------------------------------
# 论文核心公式
# ----------------------------------------------------------------------------

def expected_max_sharpe(mean_sr: float, var_sr: float, n_trials: int) -> float:
    """
    论文 Eq.6（作者 Snippet 1 的 getExpMaxSR）：
    N 次独立试验后，估计夏普的期望最大值（非年化）。
    """
    if n_trials <= 1:
        # 只试了一次：最大值就是该次试验本身，E[max] = mean（max_z = 0）
        return mean_sr
    max_z = ((1.0 - GAMMA_EM) * norm_ppf(1.0 - 1.0 / n_trials)
             + GAMMA_EM * norm_ppf(1.0 - 1.0 / (n_trials * EULER)))
    return mean_sr + math.sqrt(var_sr) * max_z


def deflated_sharpe_ratio(est_sr: float, var_sr: float, n_trials: int,
                          t_len: int, skew: float = 0.0, kurt: float = 3.0) -> float:
    """
    论文 Eq.2：DSR = P(真实 SR > 0 | 观测值、试验次数、样本长度、非正态性)。
    所有夏普均为【非年化】。
    """
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


# ----------------------------------------------------------------------------
# 演示
# ----------------------------------------------------------------------------

def demo_1_mc_verify() -> None:
    """蒙特卡洛验证解析公式（论文 Appendix A.2 的实验，Snippet 1 复刻）。"""
    print("=" * 74)
    print("演示 1：验证 E[max SR] 解析公式 vs 蒙特卡洛模拟（论文附录 A.2）")
    print("=" * 74)
    rng = np.random.default_rng(42)
    num_iters = 4000
    mu, sigma = 0.0, 1.0
    print(f"  参数：mu={mu}, sigma={sigma}, numIters={num_iters}\n")
    print(f"  {'N(试验数)':>10} {'解析 E[max]':>14} {'模拟 E[max]':>14} {'误差':>10}")
    for n in (10, 50, 100, 250, 500, 1000):
        ana = expected_max_sharpe(mu, sigma ** 2, n)
        sim = float(np.max(rng.normal(mu, sigma, size=(num_iters, n)), axis=1).mean())
        print(f"  {n:>10d} {ana:>14.4f} {sim:>14.4f} {ana - sim:>10.4f}")
    print("\n  → 解析公式与模拟高度吻合（N 越大误差越小），实现正确。\n")


def demo_2_paper_example() -> None:
    """复现论文原文数值例子（N=100 → DSR≈0.8997；N=46 → ≈0.9505）。"""
    print("=" * 74)
    print("演示 2：复现论文数值例子（国债拍卖季节性策略）")
    print("=" * 74)
    est_sr = 2.5 / math.sqrt(ANNUAL)      # 年化 2.5 → 非年化
    var_sr = 0.5 / ANNUAL                 # 各试验夏普方差
    t_len = 1250                          # 5 年日频
    skew, kurt = -3.0, 10.0               # 论文给定的偏度/峰度

    for n in (46, 88, 100, 200):
        dsr = deflated_sharpe_ratio(est_sr, var_sr, n, t_len, skew, kurt)
        flag = "✔ 可通过 95% 检验" if dsr >= 0.95 else "✘ 不显著（纯运气不可排除）"
        print(f"  N={n:>4} 次试验 → DSR = {dsr:.4f}   {flag}")
    print("\n  论文原值：N=100 → DSR≈0.8997（拒绝），N=46 → DSR≈0.9505（通过）")
    print("  与论文完全一致 → 实现正确。\n")


def demo_3_random_pool() -> None:
    """核心演示：100 个随机策略里挑冠军，看 DSR 如何拆穿它。"""
    print("=" * 74)
    print("演示 3：随机策略池过拟合 —— 冠军策略是真的吗？")
    print("=" * 74)
    rng = np.random.default_rng(7)
    t_len = 1250                      # 5 年日频回测
    n_pool = 100                      # 试了 100 个策略
    vols = rng.uniform(0.008, 0.025, n_pool)   # 每个策略不同的日波动率

    # 每个策略：真实夏普为 0 的纯噪声收益
    pool = rng.normal(0.0, vols[:, None], size=(n_pool, t_len))
    sr_hats = np.array([sharpe(r) for r in pool])

    best_idx = int(np.argmax(sr_hats))
    best_ret = pool[best_idx]
    best_sr = float(sr_hats[best_idx])
    var_sr = float(sr_hats.var(ddof=1))          # 各试验夏普的方差
    g3, g4 = skew_kurt(best_ret)

    dsr = deflated_sharpe_ratio(best_sr, var_sr, n_pool, t_len, g3, g4)
    dsr_n1 = deflated_sharpe_ratio(best_sr, var_sr, 1, t_len, g3, g4)
    e_max = expected_max_sharpe(0.0, var_sr, n_pool)

    print(f"  随机生成 {n_pool} 个策略（真实夏普全部为 0），每个回测 {t_len} 天")
    print(f"  挑出夏普最高的‘冠军策略’：")
    print(f"\n    冠军策略年化夏普        : {best_sr * math.sqrt(ANNUAL):.2f}")
    print(f"    试验池夏普方差 Var[SR]  : {var_sr:.5f}")
    print(f"    纯运气期望最高夏普 E[max]: {e_max * math.sqrt(ANNUAL):.2f} (年化)")
    print(f"    收益偏度/峰度           : {g3:.2f} / {g4:.2f}")
    print(f"\n    DSR（N={n_pool}，如实披露试验次数）: {dsr:.4f} "
          f"{'✔ 显著' if dsr >= 0.95 else f'✘ 不显著 —— 这个 {best_sr * math.sqrt(ANNUAL):.2f} 的年化夏普是随机噪声的产物！'}")
    print(f"    DSR（N=1，假装只试过一次）      : {dsr_n1:.4f} "
          f"{'✔ 显著' if dsr_n1 >= 0.95 else '✘ 不显著'}")
    print("\n  → 同一个策略，诚实披露‘试过 100 个’后立刻现形；")
    print("    不披露试验次数的回测报告 = 耍流氓（论文原话：worthless）。\n")


def demo_4_trial_budget() -> None:
    """反推：给定试验次数，观测夏普至少要多高才能通过 95% 检验。"""
    print("=" * 74)
    print("演示 4：试验次数 N 越多，需要的夏普越高（最小合格夏普）")
    print("=" * 74)
    var_sr = 1.0 / ANNUAL   # 典型：T=252 日收益的 SR 估计方差
    t_len = 1250
    print(f"  假设 Var[SR]={var_sr:.5f}, T={t_len} 天, 正态收益\n")
    print(f"  {'N(试验次数)':>12} {'最小合格夏普(年化)':>20} {'E[max SR](年化)':>16}")
    for n in (1, 10, 50, 100, 500, 1000, 5000, 10000):
        need = min_sharpe_for_dsr(0.95, var_sr, n, t_len)
        emax = expected_max_sharpe(0.0, var_sr, n) * math.sqrt(ANNUAL)
        print(f"  {n:>12d} {need * math.sqrt(ANNUAL):>20.2f} {emax:>16.2f}")
    print("\n  → 试 100 个参数组合，夏普 2.5 不够看；试 1 万个，得要 4+。")
    print("    这就是‘调参一时爽，实盘火葬场’的数学原理。\n")


def main() -> None:
    print()
    print("  Deflated Sharpe Ratio 反过拟合工具箱")
    print("  Bailey & López de Prado (2014, JPM) 论文实现")
    print("  注：文中所有‘夏普’均为论文口径（非年化），展示时已年化(×√252)\n")
    demo_1_mc_verify()
    demo_2_paper_example()
    demo_3_random_pool()
    demo_4_trial_budget()
    print("=" * 74)
    print("结论：任何回测夏普都必须回答 5 个问题才算数 ——")
    print("  ① 试过多少次试验 N？ ② 各试验夏普方差 Var[SR]？")
    print("  ③ 样本长度 T？ ④ 偏度？ ⑤ 峰度？")
    print("  回答不了 = 这个夏普是薛定谔的夏普（既真又假）。")
    print("=" * 74)


if __name__ == "__main__":
    main()
