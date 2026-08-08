#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
菜三：Almgren-Chriss 最优执行模拟器
====================================
对应学习内容：
  - 《AI量化交易从0到1》第 19 课（执行系统：滑点、冲击、成交模拟器）
  - arXiv:1906.11046（Multi-Agent DRL for Liquidation，把 AC 模型当环境）
  - Almgren & Chriss (2000) "Optimal Execution of Portfolio Transactions"

核心思想（一句话）：
  卖得越快 → 市场冲击越大；卖得越慢 → 价格风险越大。
  AC 模型把两者放进效用函数 U = E[cost] + lambda * Var[cost]，
  给出【闭式最优清仓轨迹】x_k = X * sinh(kappa*(T-t_k)) / sinh(kappa*T)。

模型设定（卖出 X 股，N 期，每期时长 tau，价格初始 S0）：
  S_k     = S_{k-1} + sigma*sqrt(tau)*xi_k - gamma*tau*v_k   （永久冲击：卖压压低价格）
  S~_k    = S_k - eps - eta*v_k                               （临时冲击 + 价差）
  cost    = sum_k v_k*(S0 - S~_k)                             （实现成本，>0 为亏损）
  其中 v_k 为第 k 期卖出速率（股/期），kappa = sqrt(lambda*sigma^2/eta)。

仅依赖 numpy，Python >= 3.8。运行：python3 almgren_chriss.py
"""

import numpy as np

# ----------------------------------------------------------------------------
# 策略轨迹
# ----------------------------------------------------------------------------

def trajectory_ac_kt(x_total: float, n_periods: int, kt: float) -> np.ndarray:
    """
    AC 闭式最优清仓轨迹，按无量纲参数 kt = kappa*T 扫描（AC 论文 Fig.3 画法）。
    kt 越大 → 越激进（前期卖得多）；kt→0 退化为线性 TWAP。
    """
    t = np.arange(n_periods + 1) / n_periods          # 归一化时间 0..1
    x = x_total * np.sinh(kt * (1.0 - t)) / np.sinh(kt)
    return x


def trajectory_linear(x_total: float, n_periods: int) -> np.ndarray:
    """线性/TWAP 基准：每期卖 X/N 股。"""
    return x_total * np.linspace(1.0, 0.0, n_periods + 1)


def trajectory_immediate(x_total: float, n_periods: int) -> np.ndarray:
    """立即执行基准：第 1 期全部卖光。"""
    x = np.zeros(n_periods + 1)
    x[0] = x_total
    return x


def kt_of_lambda(lam: float, sigma: float, eta: float, n_periods: int, tau: float) -> float:
    """由风险厌恶 lambda 换算 kt（帮助读者理解 λ 的尺度）。"""
    kappa = np.sqrt(lam * sigma ** 2.0 / eta)
    return float(kappa * n_periods * tau)


# ----------------------------------------------------------------------------
# 蒙特卡洛模拟
# ----------------------------------------------------------------------------

def simulate_cost(x_traj: np.ndarray, sigma: float, gamma: float,
                  eta: float, eps: float, tau: float, rng: np.random.Generator,
                  n_sims: int = 5000) -> tuple[float, float, np.ndarray]:
    """
    沿轨迹 x_k 模拟 n_sims 条价格路径，返回 (E[cost], std[cost], 各期卖出速率)。
    成本 = Σ v_k*(S0 - 成交价)，>0 表示卖出实现的亏损（成本）。
    """
    n = len(x_traj) - 1
    v = -np.diff(x_traj) / tau                    # 每期卖出速率（>0）
    s0 = 100.0
    costs = np.zeros(n_sims)
    for i in range(n_sims):
        xi = rng.normal(0.0, 1.0, n)
        price = s0
        fill_cost = 0.0
        for k in range(n):
            price = price + sigma * np.sqrt(tau) * xi[k] - gamma * tau * v[k]
            fill = price - eps - eta * v[k]       # 实际成交价（卖出视角，滑点不利）
            fill_cost += v[k] * (s0 - fill)
        costs[i] = fill_cost
    return float(costs.mean()), float(costs.std(ddof=1)), v


# ----------------------------------------------------------------------------
# 演示
# ----------------------------------------------------------------------------

def demo_trajectories() -> None:
    print("=" * 76)
    print("演示 1：AC 最优执行轨迹 —— kt(=κ·T) 决定清仓节奏")
    print("=" * 76)
    X, N = 100_000.0, 10
    print(f"  参数：X={X:,.0f} 股, N={N} 期, κ·T 无量纲扫描\n")
    print(f"  {'策略':<20}{'第1期卖%':>10}{'第3期卖%':>10}{'前3期累计%':>12}")
    for kt in (0.5, 1.0, 2.0, 4.0):
        x = trajectory_ac_kt(X, N, kt)
        v = -np.diff(x)
        print(f"  {'AC κT=%.1f' % kt:<20}{v[0] / X * 100:>9.1f}%{v[2] / X * 100:>9.1f}%"
              f"{v[:3].sum() / X * 100:>11.1f}%")
    x = trajectory_linear(X, N)
    v = -np.diff(x)
    print(f"  {'线性 TWAP (κT→0)':<20}{v[0] / X * 100:>9.1f}%{v[2] / X * 100:>9.1f}%"
          f"{v[:3].sum() / X * 100:>11.1f}%")
    print("\n  κ 与风险厌恶 λ 的换算（σ=2%/期, η=1e-6 时）：")
    for lam in (1e-4, 1e-3, 1e-2):
        print(f"    λ={lam:<8} → κT = {kt_of_lambda(lam, 0.02, 1e-6, N, 1.0):.1f}"
              f"{'  (激进)' if lam >= 1e-2 else ''}")
    print("  → λ 越大（越怕价格风险）→ κT 越大 → 前期卖得越多；λ→0 退化为 TWAP。\n")


def demo_risk_cost_tradeoff() -> None:
    print("=" * 76)
    print("演示 2：成本-风险权衡 —— 没有免费午餐，只有可选择的代价")
    print("=" * 76)
    X, N, tau = 100_000.0, 10, 1.0
    sigma, gamma, eta, eps = 0.02, 5e-7, 1e-6, 0.005
    rng = np.random.default_rng(42)
    n_sims = 5000

    print(f"  蒙特卡洛 {n_sims} 条价格路径（S0=100），永久冲击 γ={gamma:.0e}/股/期，"
          f"临时冲击 η={eta:.0e}/股/期，价差半宽 ε={eps}\n")
    print(f"  {'策略':<22}{'E[cost](美元)':>14}{'std[cost](美元)':>16}{'成本/成交额':>12}")

    strategies = [("立即全部卖出", trajectory_immediate(X, N))]
    for kt in (0.5, 2.0, 4.0):
        strategies.append((f"AC κT={kt}", trajectory_ac_kt(X, N, kt)))
    strategies.append(("线性 TWAP", trajectory_linear(X, N)))

    results = []
    for name, x in strategies:
        m, s, _ = simulate_cost(x, sigma, gamma, eta, eps, tau, rng, n_sims)
        results.append((name, m, s))
        print(f"  {name:<22}{m:>14,.0f}{s:>16,.0f}{m / (X * 100) * 100:>11.3f}%")

    print("\n  解读（成本 vs 风险是此消彼长的帕累托前沿）：")
    for name, m, s in results:
        print(f"  · {name:<12} 成本 {m:>7,.0f} ｜ 风险 {s:>6,.0f}")
    print("  · 立即卖出：冲击成本最大、价格风险最小；TWAP 反之")
    print("  · AC 最优 = 在 (成本, 风险) 平面上按你的 λ 选切点；")
    print("    这既是 Execution Agent 的基准，也是 1906.11046 把 AC 当 RL 环境的原因。\n")


def demo_frequency_sensitivity() -> None:
    print("=" * 76)
    print("演示 3：第19课警告 —— 高频策略对摩擦成本极度敏感")
    print("=" * 76)
    # 纯账本：固定摩擦（价差/手续费）按笔数线性放大
    print("  日成交额 100 万元，每笔固定摩擦 = 成交额的 1bp（价差+手续费）：\n")
    print(f"  {'交易频率':<20}{'每天笔数':>8}{'每天固定摩擦':>12}{'年化磨损(250天)':>16}")
    for name, n_per_day in (("日频", 1), ("小时频", 8), ("5分钟频", 48), ("1分钟频", 240)):
        per_day = n_per_day * 1e6 * 1e-4          # 每笔 1bp
        yearly = per_day * 250
        print(f"  {name:<20}{n_per_day:>8d}{per_day:>12,.0f} 元{yearly:>14,.0f} 元")
    print("\n  → 同样一套策略，只把频率从日频提到 1 分钟频，固定摩擦就放大 240 倍；")
    print("    若回测假设零滑点（K 线 Close 成交），高频策略必然沦为手续费收割机")
    print("    ——第 19 课 19.4.2 的原话警告。\n")

    # MC 确认：同一份日 alpha 拆成越多笔，固定摩擦吞噬越多
    rng = np.random.default_rng(3)
    print("  账本验证：日 alpha 固定 2,000 元，拆成 n 笔执行，每笔固定摩擦 1bp+冲击 0.5bp：")
    print(f"  {'频率':<12}{'笔数/日':>8}{'日毛alpha':>10}{'日摩擦':>10}{'日净收益':>10}")
    for name, n_per_day in (("日频", 1), ("5分钟频", 48)):
        gross = 2000.0
        friction = n_per_day * 1e6 * (1e-4 + 0.5e-4)
        print(f"  {name:<12}{n_per_day:>8d}{gross:>10,.0f}{friction:>10,.0f}{gross - friction:>10,.0f}")
    print("  → 同一份 alpha：日频净赚 1,850，5分钟频倒亏 5,200。")
    print("    这就是‘多做一笔=多赚一笔’只在零摩擦幻觉里成立的原因。\n")


def main() -> None:
    print()
    print("  Almgren-Chriss 最优执行模拟器（第19课 + arXiv:1906.11046 + AC 2000）")
    print()
    demo_trajectories()
    demo_risk_cost_tradeoff()
    demo_frequency_sensitivity()
    print("=" * 76)
    print("一句话：显示价 ≠ 成交价。用 AC 框架把冲击与风险算清楚，")
    print("否则你的回测曲线只是幻觉（第19课：把行情价当成交价 = 训练幻觉）。")
    print("=" * 76)


if __name__ == "__main__":
    main()
