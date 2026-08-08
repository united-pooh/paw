"""无第三方优化器依赖的可复现参数数值优化。"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import numpy as np

from .backtest import backtest_trend


@dataclass(frozen=True)
class SearchSpace:
    fast: tuple[int, int] = (5, 30)
    slow: tuple[int, int] = (20, 80)
    vol_window: tuple[int, int] = (10, 30)
    target_vol: tuple[float, float] = (0.08, 0.30)
    stop_loss: tuple[float, float] = (0.04, 0.20)
    momentum_window: tuple[int, int] = (0, 30)
    momentum_threshold: tuple[float, float] = (-0.02, 0.05)


def _decode(x: np.ndarray, space: SearchSpace) -> dict[str, Any]:
    fast = int(round(x[0])); slow = int(round(x[1]))
    if slow <= fast:
        slow = fast + 2
    return {
        "fast": fast,
        "slow": slow,
        "vol_window": int(round(x[2])),
        "target_vol": float(x[3]),
        "max_weight": 1.0,
        "fee_bp": 5.0,
        "stop_loss": float(x[4]),
        "momentum_window": int(round(x[5])),
        "momentum_threshold": float(x[6]),
    }


def _clip(x: np.ndarray, space: SearchSpace) -> np.ndarray:
    bounds = np.array([
        space.fast, space.slow, space.vol_window, space.target_vol,
        space.stop_loss, space.momentum_window, space.momentum_threshold,
    ], dtype=float)
    return np.minimum(np.maximum(x, bounds[:, 0]), bounds[:, 1])


def optimize_parameters(
    rows: list[dict[str, Any]],
    *,
    space: SearchSpace | None = None,
    n_iter: int = 400,
    population: int = 24,
    seed: int = 42,
    test_fraction: float = 0.30,
) -> dict[str, Any]:
    """用随机多起点 + 坐标扰动优化训练段，返回训练/测试结果。

    目标函数不是单纯收益：加入回撤、交易次数和夏普上限惩罚，降低短样本
    少量交易制造虚高夏普的概率。参数最终仍按离散交易规则落地。
    """
    if space is None:
        space = SearchSpace()
    if len(rows) < 100:
        raise ValueError("数值优化至少需要 100 条行情")
    split = max(60, int(len(rows) * (1.0 - test_fraction)))
    train, test = rows[:split], rows[split - max(space.slow[1], 30):]
    rng = np.random.default_rng(seed)
    bounds = np.array([
        space.fast, space.slow, space.vol_window, space.target_vol,
        space.stop_loss, space.momentum_window, space.momentum_threshold,
    ], dtype=float)

    def evaluate(x: np.ndarray, data: list[dict[str, Any]]) -> tuple[float, dict[str, Any] | None]:
        p = _decode(_clip(x, space), space)
        if p["slow"] >= len(data) - 2 or p["fast"] >= p["slow"]:
            return -1e9, None
        try:
            r = backtest_trend(data, **p)
        except (ValueError, ZeroDivisionError):
            return -1e9, None
        # annualized Sharpe is useful but capped; reward return and penalize risk/complexity.
        score = (
            r["total_return_pct"]
            + 0.15 * min(r["annualized_sharpe"], 3.0)
            + 0.05 * r["max_drawdown_pct"]
            - 0.015 * max(r["trade_count"] - 12, 0)
        )
        return float(score), r

    pop = rng.uniform(bounds[:, 0], bounds[:, 1], size=(population, bounds.shape[0]))
    scored = [(evaluate(x, train), x) for x in pop]
    scored.sort(key=lambda z: z[0][0], reverse=True)
    best_score, best_result = scored[0][0]
    best_x = scored[0][1].copy()
    sigma = (bounds[:, 1] - bounds[:, 0]) * 0.15
    for _ in range(n_iter):
        elite = scored[: max(2, population // 4)]
        parent = elite[rng.integers(len(elite))][1]
        candidate = _clip(parent + rng.normal(0, sigma), space)
        item = (evaluate(candidate, train), candidate)
        scored.append(item)
        scored.sort(key=lambda z: z[0][0], reverse=True)
        scored = scored[:population]
        if item[0][0] > best_score:
            best_score, best_result, best_x = item[0][0], item[0][1], candidate.copy()
        sigma *= 0.997
    params = _decode(best_x, space)
    _, train_result = evaluate(best_x, train)
    _, test_result = evaluate(best_x, test)
    return {
        "params": params,
        "objective": round(best_score, 6),
        "train_rows": len(train),
        "test_rows": len(test),
        "train_result": train_result,
        "test_result": test_result,
        "seed": seed,
        "iterations": n_iter,
        "note": "训练段优化；测试段仅用于验证，仍可能受单标的短样本影响。",
    }


__all__ = ["SearchSpace", "optimize_parameters"]
