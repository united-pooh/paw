"""Almgren-Chriss 最优执行：轨迹、成本-风险蒙特卡洛模拟（第19课 + AC 2000）。"""

import numpy as np


def trajectory_ac_kt(x_total: float, n_periods: int, kt: float) -> np.ndarray:
    """AC 闭式最优清仓轨迹 x_k（kt = kappa*T 无量纲；kt 越大越激进）。"""
    t = np.arange(n_periods + 1) / n_periods
    return x_total * np.sinh(kt * (1.0 - t)) / np.sinh(kt)


def trajectory_linear(x_total: float, n_periods: int) -> np.ndarray:
    """线性/TWAP 基准。"""
    return x_total * np.linspace(1.0, 0.0, n_periods + 1)


def trajectory_immediate(x_total: float, n_periods: int) -> np.ndarray:
    """立即全部卖出基准。"""
    x = np.zeros(n_periods + 1)
    x[0] = x_total
    return x


def simulate_cost(x_traj: np.ndarray, sigma: float, gamma: float,
                  eta: float, eps: float, tau: float = 1.0,
                  n_sims: int = 2000, seed: int = 42) -> dict:
    """沿轨迹模拟 n_sims 条价格路径，返回 (E[cost], std[cost], 各期速率)。"""
    n = len(x_traj) - 1
    v = -np.diff(x_traj) / tau
    s0 = 100.0
    rng = np.random.default_rng(seed)
    costs = np.zeros(n_sims)
    for i in range(n_sims):
        xi = rng.normal(0.0, 1.0, n)
        price = s0
        total = 0.0
        for k in range(n):
            price = price + sigma * np.sqrt(tau) * xi[k] - gamma * tau * v[k]
            fill = price - eps - eta * v[k]
            total += v[k] * (s0 - fill)
        costs[i] = total
    return {
        "expected_cost": float(costs.mean()),
        "std_cost": float(costs.std(ddof=1)),
        "fill_rates": [float(x) for x in v],
    }


def compare_strategies(x_total: float, n_periods: int, sigma: float,
                       gamma: float, eta: float, eps: float, tau: float = 1.0,
                       kt_values: list[float] | None = None,
                       n_sims: int = 2000, seed: int = 42) -> dict:
    """对比：立即卖出 / AC(各 kt) / 线性 TWAP 的成本-风险。"""
    if kt_values is None:
        kt_values = [0.5, 2.0, 4.0]
    strategies = [("immediate", trajectory_immediate(x_total, n_periods))]
    for kt in kt_values:
        strategies.append((f"ac_kt_{kt}", trajectory_ac_kt(x_total, n_periods, kt)))
    strategies.append(("twap", trajectory_linear(x_total, n_periods)))
    out = []
    for name, x in strategies:
        res = simulate_cost(x, sigma, gamma, eta, eps, tau, n_sims, seed)
        out.append({"strategy": name, **res})
    return {"x_total": x_total, "n_periods": n_periods, "results": out}
