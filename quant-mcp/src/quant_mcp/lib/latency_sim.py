"""延迟与队列对做市成交的影响模拟（hftbacktest "Impact of Order Latency" 教程复刻）。

官方实测（ETHUSDT 2023-04-01~05，GLFT 做市）：
  feed 延迟回测 SR −0.20 | 真实 order latency SR +1.54 | 放大延迟 SR −0.38
  —— 同一策略同一行情，延迟模型不同，结论从"赚"变"亏"。

本模拟器用简化 tick 世界复现这个定性结论：
  - mid 随机游走（可带方向性漂移/跳跃）
  - 市价单流带"信息性"：买单在上涨后更密集（逆向选择来源）
  - 做市商每 K tick 以当刻 mid±half_spread 挂单；订单在 submit_t + latency 后才生效
  - 市价单深度超过挂单价且订单已生效 → 成交；成交后重新挂单
对比三种延迟配置的 PnL / SR / 成交数 / 库存路径。
"""

import numpy as np


def _rng(seed: int | None) -> np.random.Generator:
    return np.random.default_rng(seed)


def simulate_market(
    n_steps: int = 4000,
    tick_size: float = 0.1,
    sigma_per_step: float = 0.3,
    drift: float = 0.0,
    arrival_prob: float = 0.25,
    info_strength: float = 2.0,
    seed: int | None = 42,
) -> dict:
    """生成简化 tick 市场：mid 路径 + 市价单流（含信息性方向）。

    Args:
        n_steps: tick 步数（每步 = 100ms 量级）
        tick_size: 价格最小步长
        sigma_per_step: mid 每步波动（tick 数）
        drift: 每步漂移（tick 数，>0 上涨趋势）
        arrival_prob: 每步市价单到达概率
        info_strength: 信息强度——市价单方向与"上一段收益"的耦合（逆向选择来源）
        seed: 随机种子

    Returns:
        dict: mid_tick 路径、市价单事件（step, side, depth_tick）
    """
    rng = _rng(seed)
    mid_tick = np.zeros(n_steps)
    mid_tick[0] = 100_000.0
    for t in range(1, n_steps):
        mid_tick[t] = mid_tick[t - 1] + drift + rng.normal(0.0, sigma_per_step)

    trades = []  # (step, side(+1 buy), depth_tick)
    for t in range(n_steps):
        if rng.random() < arrival_prob:
            # 信息性方向：近期收益的 sigmoid 概率（buy 更可能在上涨后）
            lookback = max(0, t - 20)
            ret = mid_tick[t] - mid_tick[lookback]
            p_buy = 1.0 / (1.0 + np.exp(-info_strength * ret / max(sigma_per_step, 1e-9)))
            side = 1 if rng.random() < p_buy else -1
            # 深度：指数衰减随机（λ(δ) = A·e^{-kδ} 的精神）
            depth = rng.exponential(4.0) + rng.random()
            trades.append((t, side, float(depth)))
    return {"mid_tick": mid_tick.tolist(), "trades": trades}


def run_market_maker(
    mid_tick: np.ndarray,
    trades: list,
    latency_steps: int = 0,
    half_spread_tick: float = 5.0,
    order_qty: float = 1.0,
    rebalance_every: int = 5,
    rebate: float = 0.0,
    seed: int | None = 42,
) -> dict:
    """单次做市回放：给定延迟（步数）跑出 PnL。

    规则（简化自 hftbacktest 教程）：
      - 每 rebalance_every 步，以当刻 mid ± half_spread 提交一买一卖（GTX 精神：不主动吃单）
      - 订单在提交后 latency_steps 步才生效（order latency + feed latency 合并）
      - 市价单到达时：买(mo buy)打穿 ask 侧 → 若做市 ask 价 ≤ 成交价且已生效 → 成交卖出
        卖(mo sell)打穿 bid 侧 → 若做市 bid 价 ≥ 成交价且已生效 → 成交买入
      - 成交后该侧订单作废，下一轮重挂；未成交订单若价格已不适配（新 mid 距离 > 2×half_spread）则撤单重挂
      - 返佣 rebate：每笔成交按名义价值 × rebate 入账（官方 -0.00005 = 0.005%）

    Returns:
        dict: pnl、sr（每步收益的夏普）、n_fills、final_position、fill_prices 摘要
    """
    rng = _rng(seed)
    n = len(mid_tick)
    # 做市挂单：dict price_tick -> (side, active_step)
    bid_order: dict = {}  # price -> active_at
    ask_order: dict = {}
    position = 0.0
    cash = 0.0
    fills = 0
    pnl_path = np.zeros(n)
    pos_path = np.zeros(n)
    # mid_tick 已是 tick 单位；价格 = tick × tick_size（本模拟 tick_size=1）

    trade_by_step: dict[int, list] = {}
    for (t, side, depth) in trades:
        trade_by_step.setdefault(t, []).append((side, depth))

    for t in range(n):
        mid = float(mid_tick[t])

        # ---- 报价更新（每 rebalance_every 步）----
        if t % rebalance_every == 0:
            bid_px = round(mid - half_spread_tick)
            ask_px = round(mid + half_spread_tick)
            active_at = t + latency_steps
            # 撤掉价格已不适配的旧单
            for px in [p for p in bid_order if abs(p - (mid - half_spread_tick)) > half_spread_tick]:
                del bid_order[px]
            for px in [p for p in ask_order if abs(p - (mid + half_spread_tick)) > half_spread_tick]:
                del ask_order[px]
            # 保持一买一卖（若该侧无单）
            if not bid_order:
                bid_order[bid_px] = active_at
            if not ask_order:
                ask_order[ask_px] = active_at

        # ---- 市价单撮合 ----
        for (side, depth) in trade_by_step.get(t, []):
            if side > 0:  # 买方市价单：吃 ask 侧，成交价 = mid + depth×tick
                exec_px = mid + depth
                for px in sorted(ask_order):
                    if ask_order[px] <= t and px <= exec_px:
                        cash += px * order_qty
                        position -= order_qty
                        fills += 1
                        if rebate:
                            cash += px * order_qty * rebate
                        del ask_order[px]
                        break
            else:  # 卖方市价单：吃 bid 侧
                exec_px = mid - depth
                for px in sorted(bid_order, reverse=True):
                    if bid_order[px] <= t and px >= exec_px:
                        cash -= px * order_qty
                        position += order_qty
                        fills += 1
                        if rebate:
                            cash += px * order_qty * rebate
                        del bid_order[px]
                        break

        # ---- 盯市 PnL ----
        pnl_path[t] = cash + position * mid
        pos_path[t] = position

    sr = float(np.nanmean(np.diff(pnl_path)) / (np.nanstd(np.diff(pnl_path)) + 1e-12))
    return {
        "pnl": float(pnl_path[-1]),
        "sr_per_step": round(sr, 4),
        "n_fills": fills,
        "final_position": position,
        "max_abs_position": float(np.max(np.abs(pos_path))),
    }


def simulate_latency_impact(
    n_steps: int = 4000,
    sigma_per_step: float = 0.3,
    arrival_prob: float = 0.25,
    info_strength: float = 2.0,
    half_spread_tick: float = 5.0,
    latency_steps: list[int] | None = None,
    rebate: float = 0.0,
    seed: int | None = 42,
) -> dict:
    """对比不同延迟配置下的做市表现（复刻官方"延迟决定生死"结论）。

    Args:
        n_steps: tick 步数
        sigma_per_step: mid 波动（tick/步）
        arrival_prob: 市价单到达概率
        info_strength: 信息流强度（>0 制造逆向选择）
        half_spread_tick: 做市半价差（tick）
        latency_steps: 要对比的延迟配置（步数），默认 [0, 3, 10]
        rebate: 每笔成交返佣比例（0.00005 = 0.005% maker rebate）
        seed: 随机种子

    Returns:
        dict: 各配置的 pnl/sr/fills + 定性结论
    """
    if latency_steps is None:
        latency_steps = [0, 3, 10]
    market = simulate_market(
        n_steps=n_steps, sigma_per_step=sigma_per_step, drift=0.0,
        arrival_prob=arrival_prob, info_strength=info_strength, seed=seed,
    )
    mid = np.asarray(market["mid_tick"], dtype=np.float64)
    trades = market["trades"]

    results = []
    for lat in latency_steps:
        r = run_market_maker(
            mid, trades, latency_steps=int(lat),
            half_spread_tick=half_spread_tick, rebate=rebate, seed=seed,
        )
        r["latency_steps"] = int(lat)
        results.append(r)

    # 定性结论：低延迟应显著优于高延迟
    pnls = [r["pnl"] for r in results]
    verdict = (
        "延迟显著恶化做市收益：成交时价格已移动（逆向选择）。"
        if pnls[0] > pnls[-1]
        else "本组参数下延迟影响不明显——信息流强度 info_strength 越高差异越大"
    )
    return {
        "market": {
            "n_steps": n_steps,
            "n_trades": len(trades),
            "sigma_per_step": sigma_per_step,
            "info_strength": info_strength,
        },
        "configs": [
            {
                "latency_steps": r["latency_steps"],
                "pnl": round(r["pnl"], 4),
                "sr_per_step": r["sr_per_step"],
                "n_fills": r["n_fills"],
                "final_position": round(r["final_position"], 2),
            }
            for r in results
        ],
        "verdict": verdict,
        "reference": {
            "official_eth_example": [
                {"latency_model": "feed 延迟生成", "sr": -0.20},
                {"latency_model": "真实 order latency", "sr": 1.54},
                {"latency_model": "放大 feed 延迟", "sr": -0.38},
            ],
            "note": "官方教程（ETHUSDT 2023-04 五天 GLFT 做市）：延迟模型不同，同一策略从亏变赚。"
                    "回测里没有延迟 = 凭空多出几毫秒 alpha。",
        },
    }
