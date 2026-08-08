"""微观结构信号（hftbacktest Pricing Framework 复刻）：OBI / VAMP / Effective-VAMP / Weighted-Depth。

官方 precompute_obi 的加速核心：只需累计 4 个量（ΣQ_bid、Σd·Q_bid、ΣQ_ask、Σd·Q_ask），
任意深度范围即取即用。本模块将其向量化为 numpy 版本：

  VAMP = tick×(mid·ΣQ_ask − Σd·Q_ask + mid·ΣQ_bid + Σd·Q_bid) / (ΣQ_bid + ΣQ_ask)   # 交叉乘
  bid_eff = tick×(mid·ΣQ_bid − Σd·Q_bid) / ΣQ_bid                                     # 同侧加权均价
  ask_eff = tick×(mid·ΣQ_ask + Σd·Q_ask) / ΣQ_ask
  EffVAMP = (bid_eff·ΣQ_ask + ask_eff·ΣQ_bid) / (ΣQ_bid + ΣQ_ask)

输入为 L2 快照序列：bids[i] = 第 i 个快照的 bid 深度（从 best 向下），asks[i] 同理。
"""

import numpy as np


def _validate_snapshots(bids: list[list[float]], asks: list[list[float]]) -> tuple[np.ndarray, np.ndarray]:
    if len(bids) != len(asks):
        raise ValueError(f"bids 与 asks 快照数不一致：{len(bids)} vs {len(asks)}")
    if len(bids) == 0:
        raise ValueError("快照列表为空")
    b = [np.asarray(x, dtype=np.float64) for x in bids]
    a = [np.asarray(x, dtype=np.float64) for x in asks]
    for i, (bb, aa) in enumerate(zip(b, a)):
        if len(bb) == 0 or len(aa) == 0:
            raise ValueError(f"快照 {i} 为空")
        if np.any(bb < 0) or np.any(aa < 0):
            raise ValueError(f"快照 {i} 含负数量")
        if np.any(~np.isfinite(bb)) or np.any(~np.isfinite(aa)):
            raise ValueError(f"快照 {i} 含非有限值")
    return b, a


def aggregate_snapshot(
    bid_prices: np.ndarray, bid_qty: np.ndarray,
    ask_prices: np.ndarray, ask_qty: np.ndarray,
    mid_price: float | None = None,
    depth_range: float = 0.025,
    tick_size: float = 1.0,
) -> dict:
    """单个 L2 快照的全部微观结构信号。

    Args:
        bid_prices / bid_qty: bid 侧价格与数量（从 best 向下，长度一致）
        ask_prices / ask_qty: ask 侧价格与数量（从 best 向上）
        mid_price: 中点价；None 时取 (best_bid+best_ask)/2
        depth_range: 聚合深度范围（相对 mid 的比例，如 0.025 = 2.5%）
        tick_size: 价格最小步长（用于 VAMP 的 tick 分解；仅影响返回的 tick 语义，默认 1 不影响数值）

    Returns:
        dict: mid, spread, OBI, VAMP, bid_eff, ask_eff, vamp_eff, weighted_depth_bid/ask,
              各量的 tick 分解（mid_tick, sum_q_bid, sum_dq_bid, sum_q_ask, sum_dq_ask）
    """
    bid_prices = np.asarray(bid_prices, dtype=np.float64)
    bid_qty = np.asarray(bid_qty, dtype=np.float64)
    ask_prices = np.asarray(ask_prices, dtype=np.float64)
    ask_qty = np.asarray(ask_qty, dtype=np.float64)
    if len(bid_prices) != len(bid_qty) or len(ask_prices) != len(ask_qty):
        raise ValueError("价格与数量长度不一致")
    if len(bid_prices) == 0 or len(ask_prices) == 0:
        raise ValueError("盘口为空")

    best_bid = float(bid_prices[0])
    best_ask = float(ask_prices[0])
    if best_bid >= best_ask:
        raise ValueError(f"盘口交叉：best_bid={best_bid} >= best_ask={best_ask}")
    mid = mid_price if mid_price is not None else (best_bid + best_ask) / 2.0

    # 截取深度范围内：bid >= mid×(1−depth_range)，ask <= mid×(1+depth_range)
    bid_lo = mid * (1.0 - depth_range)
    ask_hi = mid * (1.0 + depth_range)
    bm = bid_prices >= bid_lo
    am = ask_prices <= ask_hi
    bp, bq = bid_prices[bm], bid_qty[bm]
    ap, aq = ask_prices[am], ask_qty[am]

    mid_tick = mid / tick_size
    # d = |price − mid|（tick 数）
    d_bid = (mid - bp) / tick_size
    d_ask = (ap - mid) / tick_size

    sum_q_bid = float(bq.sum())
    sum_q_ask = float(aq.sum())
    sum_dq_bid = float((d_bid * bq).sum())
    sum_dq_ask = float((d_ask * aq).sum())

    denom = sum_q_bid + sum_q_ask
    if denom <= 0:
        raise ValueError("深度范围内买卖总量为零")

    vamp = tick_size * (mid_tick * sum_q_ask - sum_dq_ask + mid_tick * sum_q_bid + sum_dq_bid) / denom
    bid_eff = tick_size * (mid_tick * sum_q_bid - sum_dq_bid) / sum_q_bid if sum_q_bid > 0 else np.nan
    ask_eff = tick_size * (mid_tick * sum_q_ask + sum_dq_ask) / sum_q_ask if sum_q_ask > 0 else np.nan
    vamp_eff = (bid_eff * sum_q_ask + ask_eff * sum_q_bid) / denom

    # Weighted-Depth 价：同侧 Σ(P·Q)/ΣQ（官方 Weighted-Depth Order Book Price）
    wd_bid = float((bp * bq).sum()) / sum_q_bid if sum_q_bid > 0 else np.nan
    wd_ask = float((ap * aq).sum()) / sum_q_ask if sum_q_ask > 0 else np.nan

    return {
        "mid": round(mid, 6),
        "spread": round(best_ask - best_bid, 6),
        "obi": round(sum_q_bid - sum_q_ask, 6),
        "vamp": round(vamp, 6),
        "bid_eff": None if np.isnan(bid_eff) else round(bid_eff, 6),
        "ask_eff": None if np.isnan(ask_eff) else round(ask_eff, 6),
        "vamp_eff": round(vamp_eff, 6),
        "weighted_depth_bid": None if np.isnan(wd_bid) else round(wd_bid, 6),
        "weighted_depth_ask": None if np.isnan(wd_ask) else round(wd_ask, 6),
        "tick_decomp": {
            "mid_tick": round(mid_tick, 2),
            "sum_q_bid": round(sum_q_bid, 6),
            "sum_dq_bid": round(sum_dq_bid, 6),
            "sum_q_ask": round(sum_q_ask, 6),
            "sum_dq_ask": round(sum_dq_ask, 6),
            "depth_range": depth_range,
        },
    }


def compute_signals_series(
    bid_prices: list[list[float]],
    bid_qty: list[list[float]],
    ask_prices: list[list[float]],
    ask_qty: list[list[float]],
    depth_range: float = 0.025,
    tick_size: float = 1.0,
    std_window: int | None = None,
) -> dict:
    """多快照序列的微观结构信号 + 标准化 OBI + VAMP 收益率。

    Args:
        bid_prices / bid_qty / ask_prices / ask_qty: 每元素为一个快照（同构列表）
        depth_range: 聚合深度范围（0.025 = 2.5%）
        tick_size: tick 大小
        std_window: 标准化窗口（快照数）；None = 全序列（官方 1h/1s = 3600 个 100ms 快照）

    Returns:
        dict: 每快照的 mid/obi/vamp/vamp_eff + vamp_ret/vamp_eff_ret + std_obi 序列
    """
    b_p, a_p = _validate_snapshots(bid_prices, ask_prices)  # 校验 bid/ask 数量一致
    if len(bid_qty) != len(b_p) or len(ask_qty) != len(a_p):
        raise ValueError("价格与数量快照数不一致")
    n = len(b_p)
    mids = np.empty(n)
    obis = np.empty(n)
    vamps = np.empty(n)
    vamp_effs = np.empty(n)
    for i in range(n):
        bp = np.asarray(bid_prices[i], dtype=np.float64)
        bq = np.asarray(bid_qty[i], dtype=np.float64)
        ap = np.asarray(ask_prices[i], dtype=np.float64)
        aq = np.asarray(ask_qty[i], dtype=np.float64)
        if len(bp) != len(bq) or len(ap) != len(aq) or len(bp) == 0 or len(ap) == 0:
            raise ValueError(f"快照 {i} 的价格/数量长度非法")
        best_bid, best_ask = float(bp[0]), float(ap[0])
        if best_bid >= best_ask:
            raise ValueError(f"快照 {i} 盘口交叉")
        mid = (best_bid + best_ask) / 2.0
        bid_lo, ask_hi = mid * (1 - depth_range), mid * (1 + depth_range)
        bp, bq = bp[bp >= bid_lo], bq[bp >= bid_lo]
        ap, aq = ap[ap <= ask_hi], aq[ap <= ask_hi]
        mids[i] = mid
        obis[i] = float(bq.sum() - aq.sum())
        denom = float(bq.sum() + aq.sum())
        if denom <= 0:
            vamps[i] = np.nan
            vamp_effs[i] = np.nan
            continue
        mid_t = mid / tick_size
        d_bid = (mid - bp) / tick_size
        d_ask = (ap - mid) / tick_size
        s_qb, s_qa = float(bq.sum()), float(aq.sum())
        s_dqb, s_dqa = float((d_bid * bq).sum()), float((d_ask * aq).sum())
        vamps[i] = tick_size * (mid_t * s_qa - s_dqa + mid_t * s_qb + s_dqb) / denom
        bid_eff = tick_size * (mid_t * s_qb - s_dqb) / s_qb if s_qb > 0 else np.nan
        ask_eff = tick_size * (mid_t * s_qa + s_dqa) / s_qa if s_qa > 0 else np.nan
        vamp_effs[i] = (bid_eff * s_qa + ask_eff * s_qb) / denom

    # 标准化 OBI（官方：1 小时窗口 z-score；缩放因子 0.0001 对齐收益量纲）
    w = std_window if std_window is not None else max(n, 1)
    std_obi = np.full(n, np.nan)
    for i in range(n):
        lo = max(0, i + 1 - w)
        seg = obis[lo:i + 1]
        m, s = np.nanmean(seg), np.nanstd(seg)
        if s and np.isfinite(s) and np.isfinite(obis[i]):
            std_obi[i] = (obis[i] - m) / s

    vamp_ret = vamps / mids - 1.0
    vamp_eff_ret = vamp_effs / mids - 1.0
    return {
        "n_snapshots": n,
        "depth_range": depth_range,
        "std_window": w,
        "mids": [round(v, 6) for v in mids],
        "obis": [round(v, 6) for v in obis],
        "std_obi": [None if np.isnan(v) else round(v, 4) for v in std_obi],
        "vamps": [None if np.isnan(v) else round(v, 6) for v in vamps],
        "vamp_effs": [None if np.isnan(v) else round(v, 6) for v in vamp_effs],
        "vamp_returns": [None if np.isnan(v) else round(v, 8) for v in vamp_ret],
        "vamp_eff_returns": [None if np.isnan(v) else round(v, 8) for v in vamp_eff_ret],
        "reference": {
            "note": "官方 2025-08-01 BTCUSDT 单日：VAMP 0.25% 深度 SR 25.6；Eff-VAMP 0.5% SR 18.1；"
                    "标准化 OBI(2.5%, 1h 窗口) SR 21.4；BTC alpha 迁移到 ETH/XRP/SOL/DOGE 均有效",
        },
    }


def information_coefficient(signal: list[float], mids: list[float], horizon: int = 100) -> float:
    """信息系数 IC：信号与 horizon 步前向收益的 Pearson 相关（官方 ic() 的 numpy 版）。

    IC 越接近 ±1 越强；官方示例中 BTC alpha 的 IC 峰值出现在 ~10 分钟前向。
    """
    s = np.asarray(signal, dtype=np.float64)
    m = np.asarray(mids, dtype=np.float64)
    if horizon <= 0:
        raise ValueError("horizon 必须 > 0")
    if len(s) <= horizon + 2:
        raise ValueError("序列太短，无法计算前向收益相关")
    fwd = np.empty(len(m) - horizon)
    for i in range(len(m) - horizon):
        fwd[i] = m[i + horizon] / m[i] - 1.0
    a = s[:-horizon]
    valid = np.isfinite(a) & np.isfinite(fwd) & np.isfinite(m[:-horizon])
    if valid.sum() < 5:
        raise ValueError("有效样本太少（<5），无法计算 IC")
    corr = np.corrcoef(a[valid], fwd[valid])[0, 1]
    return float(corr)
