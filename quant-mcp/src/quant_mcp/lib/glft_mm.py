"""GLFT 做市模型校准（Guéant–Lehalle–Fernandez-Tapia，hftbacktest 教程实现复刻）。

纯 numpy 复刻官方教程三件套：
  1. measure_trading_intensity —— 从"市价单到达深度"直方图化交易强度 λ(δ)
  2. log-linear 回归 —— 校准 λ(δ) = A·exp(-k·δ)（仅拟合近端浅层）
  3. compute_coeff —— 论文式 (4.6)/(4.7) 的最优半价差与库存 skew

约定（与官方一致）：
  - arrival_depth：市价单相对当刻 mid 的穿透深度，单位 = tick；
    买方市价单 = 成交价tick - mid_tick，卖方市价单 = mid_tick - 成交价tick。
  - mid_price_chg：每 100ms 的 mid 变化（tick 数），波动率 = std × sqrt(10) 换算为 tick/sqrt(s)。
  - 快速行情中出现在 mid 另一侧的负深度直接剔除。
"""

import math

import numpy as np

INVALID = np.nan


def measure_trading_intensity(arrival_depths: np.ndarray, n_bins: int = 500) -> np.ndarray:
    """把市价单到达深度直方图化为交易强度（官方 measure_trading_intensity 原逻辑）。

    对每次到达：tick = round(depth / 0.5) - 1（半 tick 精度）；
    所有挂在"比该深度更近"的报价（不含同价）都会被这笔市价单打中，故 out[:tick] += 1。

    Args:
        arrival_depths: 市价单到达深度序列（tick 数，含 NaN 表示缺失）
        n_bins: 最大统计档位数（官方 500）

    Returns:
        长度 = max_tick 的计数数组：out[δ] = 窗口内"若挂在距 mid δ 个 tick 处会被成交"的次数
    """
    out = np.zeros(n_bins, dtype=np.float64)
    max_tick = 0
    for depth in arrival_depths:
        if not np.isfinite(depth):
            continue
        tick = int(round(depth / 0.5) - 1)
        if tick < 0 or tick >= n_bins:
            continue
        out[:tick] += 1.0
        max_tick = max(max_tick, tick)
    return out[:max_tick]


def linear_regression(x: np.ndarray, y: np.ndarray) -> tuple[float, float]:
    """普通最小二乘斜率/截距（官方原码，闭式解）。"""
    sx = float(np.sum(x))
    sy = float(np.sum(y))
    sx2 = float(np.sum(x**2))
    sxy = float(np.sum(x * y))
    w = len(x)
    denom = w * sx2 - sx**2
    if denom == 0.0:
        raise ValueError("线性回归退化：x 无变化（数据不足或窗口内无到达）")
    slope = (w * sxy - sx * sy) / denom
    intercept = (sy - slope * sx) / w
    return slope, intercept


def calibrate_intensity(
    arrival_depths: np.ndarray,
    window_seconds: float = 600.0,
    step_seconds: float = 0.1,
    fit_depth_ticks: int = 70,
) -> dict:
    """校准交易强度 λ(δ) = A·exp(-k·δ)。

    Args:
        arrival_depths: 市价单到达深度序列（tick）
        window_seconds: 校准窗口长度（官方 10 分钟 = 600s）
        step_seconds: 采样步长（官方 100ms = 0.1s）
        fit_depth_ticks: 仅拟合距 mid 这么近的档位（官方 70）

    Returns:
        dict: A（s^-1）、k（tick^-1）、lambda_curve（实际计数/秒，前 fit_depth_ticks 档）、
              ticks（对应 tick 轴）、window_steps、有效到达笔数
    """
    n_window = int(round(window_seconds / step_seconds))
    if len(arrival_depths) < 2:
        raise ValueError("arrival_depths 太短，无法校准")
    depths = np.asarray(arrival_depths, dtype=np.float64)
    window = depths[-n_window:] if len(depths) > n_window else depths

    counts = measure_trading_intensity(window)
    if len(counts) < 3:
        raise ValueError("窗口内有效到达太少（<3 档），无法拟合。请提供更长的到达深度序列")

    # 换算为每秒强度
    lam = counts[:fit_depth_ticks] / (len(window) * step_seconds)
    # 过滤零值档（ln(0) 无定义）
    valid = lam > 0.0
    if valid.sum() < 3:
        raise ValueError("近端强度全为零：市价单从未打穿近端档位，数据可能不含成交")
    x = np.arange(len(lam), dtype=np.float64)[valid] + 0.5  # 官方 tick 轴从 0.5 起
    y = np.log(lam[valid])
    slope, intercept = linear_regression(x, y)
    A = math.exp(intercept)
    k = -slope
    if A <= 0 or k <= 0:
        raise ValueError(f"校准失败：A={A:.4f}, k={k:.4f}（交易强度应随距离衰减，请检查数据）")
    return {
        "A": A,
        "k": k,
        "lambda_curve": lam.tolist(),
        "ticks": (np.arange(len(lam)) + 0.5).tolist(),
        "window_steps": len(window),
        "window_seconds": len(window) * step_seconds,
        "n_arrivals": int(np.isfinite(depths).sum()),
        "n_arrivals_used": int(np.isfinite(window).sum()),
    }


def compute_volatility(mid_price_chg: np.ndarray, step_seconds: float = 0.1) -> float:
    """mid 变化的波动率，换算为 tick/sqrt(s)（官方：std × sqrt(1/step_seconds)）。"""
    chg = np.asarray(mid_price_chg, dtype=np.float64)
    chg = chg[np.isfinite(chg)]
    if len(chg) < 5:
        raise ValueError("mid_price_chg 太短（<5），无法估计波动率")
    return float(np.std(chg, ddof=0) * math.sqrt(1.0 / step_seconds))


def compute_coeff(gamma: float, delta: float, A: float, k: float, xi: float | None = None) -> tuple[float, float]:
    """GLFT 论文式 (4.6)/(4.7) 的系数 c1、c2（官方 compute_coeff 原式）。

    c1 = 1/(ξδ)·ln(1 + ξδ/k)
    c2 = sqrt( γ/(2Aδk) · (1 + ξδ/k)^(k/(ξδ)+1) )
    """
    if gamma <= 0 or delta <= 0 or A <= 0 or k <= 0:
        raise ValueError("gamma/delta/A/k 必须为正")
    xi = xi if xi is not None else gamma  # 官方教程取 ξ = γ
    inv_k = 1.0 / k
    base = 1.0 + xi * delta * inv_k
    if base <= 0:
        raise ValueError("1 + ξδ/k <= 0，参数无意义")
    c1 = 1.0 / (xi * delta) * math.log(base)
    c2 = math.sqrt(gamma / (2.0 * A * delta * k) * base ** (k / (xi * delta) + 1.0))
    return c1, c2


def glft_quotes(
    mid_price_tick: float,
    position: float,
    gamma: float,
    delta: float,
    A: float,
    k: float,
    volatility: float,
    adj1: float = 1.0,
    adj2: float = 1.0,
    best_bid_tick: float | None = None,
    best_ask_tick: float | None = None,
) -> dict:
    """GLFT 最优报价：half_spread / skew / reservation / bid / ask（单位 tick）。

    half_spread = (c1 + δ/2·c2·σ) × adj1
    skew        = c2·σ × adj2
    reservation = mid − skew·position
    bid = min(round(reservation − half_spread), best_bid)；ask = max(round(...), best_ask)
    """
    c1, c2 = compute_coeff(gamma, delta, A, k)
    half_spread = (c1 + delta / 2.0 * c2 * volatility) * adj1
    skew = c2 * volatility * adj2
    reservation = mid_price_tick - skew * position
    bid = min(round(reservation - half_spread), best_bid_tick if best_bid_tick is not None else round(reservation - half_spread))
    ask = max(round(reservation + half_spread), best_ask_tick if best_ask_tick is not None else round(reservation + half_spread))
    return {
        "c1": c1,
        "c2": c2,
        "half_spread_tick": half_spread,
        "skew_tick": skew,
        "reservation_price_tick": reservation,
        "bid_tick": bid,
        "ask_tick": ask,
    }


def hit_probability(arrival_depths: np.ndarray, half_spread_tick: float) -> float:
    """诊断：历史市价单中有多少比例能打穿距 mid 这么远的报价（官方 percentileofscore 版）。

    官方 ETHUSDT 示例：half_spread≈20.5 tick 时该值 ≈ 1.86%（按笔数，非按量）。
    """
    depths = np.asarray(arrival_depths, dtype=np.float64)
    depths = depths[np.isfinite(depths)]
    if len(depths) == 0:
        raise ValueError("arrival_depths 为空")
    return float(np.mean(depths > half_spread_tick))


def calibrate_glft(
    arrival_depths: list[float],
    mid_price_chg: list[float],
    gamma: float = 0.05,
    delta: float = 1.0,
    step_seconds: float = 0.1,
    fit_depth_ticks: int = 70,
    adj1: float = 1.0,
    adj2: float = 0.05,
    position: float = 0.0,
    mid_price_tick: float | None = None,
) -> dict:
    """一站式 GLFT 校准：交易强度 + 波动率 + 最优报价 + 打穿概率诊断。

    Args:
        arrival_depths: 市价单到达深度（tick，相对当刻 mid；买方=成交价-mid，卖方=mid-成交价）
        mid_price_chg: 每 step_seconds 的 mid 变化（tick）
        gamma: 风险厌恶系数（官方 0.05）
        delta: 单 tick 的"价格步长"（官方 1）
        step_seconds: 采样步长秒数（官方 100ms → 0.1）
        fit_depth_ticks: 交易强度仅拟合的近端档位数（官方 70）
        adj1: 半价差调节因子（官方 1）
        adj2: skew 调节因子（官方示例 0.05，裸模型 1 会导致不敢持仓）
        position: 当前持仓（张数，用于演示 skew 对报价的偏移）
        mid_price_tick: 演示报价用的当前 mid（tick 数）

    Returns:
        dict: 校准结果 + 报价 + 打穿概率 + 参照值说明
    """
    intensity = calibrate_intensity(
        np.asarray(arrival_depths, dtype=np.float64),
        window_seconds=600.0,
        step_seconds=step_seconds,
        fit_depth_ticks=fit_depth_ticks,
    )
    volatility = compute_volatility(np.asarray(mid_price_chg, dtype=np.float64), step_seconds)
    quotes = glft_quotes(
        mid_price_tick=float(mid_price_tick) if mid_price_tick is not None else 100_000.0,
        position=position,
        gamma=gamma,
        delta=delta,
        A=intensity["A"],
        k=intensity["k"],
        volatility=volatility,
        adj1=adj1,
        adj2=adj2,
    )
    hit = hit_probability(np.asarray(arrival_depths, dtype=np.float64), quotes["half_spread_tick"])
    return {
        "A_per_sec": round(intensity["A"], 6),
        "k_per_tick": round(intensity["k"], 6),
        "volatility_tick_per_sqrt_sec": round(volatility, 4),
        "half_spread_tick": round(quotes["half_spread_tick"], 4),
        "skew_tick": round(quotes["skew_tick"], 4),
        "reservation_price_tick": round(quotes["reservation_price_tick"], 2),
        "bid_tick": quotes["bid_tick"],
        "ask_tick": quotes["ask_tick"],
        "hit_probability_pct": round(hit * 100.0, 2),
        "fit": {
            "A": round(intensity["A"], 6),
            "k": round(intensity["k"], 6),
            "n_arrivals": intensity["n_arrivals"],
            "window_seconds": intensity["window_seconds"],
            "lambda_curve_head": [round(v, 4) for v in intensity["lambda_curve"][:10]],
        },
        "reference": {
            "hit_probability_eth_example_pct": 1.86,
            "half_spread_eth_example_tick": 20.47,
            "note": "官方 ETHUSDT 示例：half_spread≈20.5 tick，仅 1.86% 的市价单能打中；"
                    "adj2=0.05 是官方将裸模型 SR(-246) 救活为 +1.2 的关键调节",
        },
    }
