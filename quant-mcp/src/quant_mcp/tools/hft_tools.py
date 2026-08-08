"""HFT 工具（hftbacktest 方法论转化）：GLFT 做市校准 / 微观结构信号 / 延迟影响模拟。"""

from mcp.server.fastmcp import FastMCP

from ..lib import glft_mm, latency_sim, microstructure


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def calibrate_glft_mm(
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
        """GLFT 做市模型校准（Guéant–Lehalle–Fernandez-Tapia，hftbacktest 教程复刻）。

        输入"市价单到达深度"与"mid 变化"两个序列，输出：交易强度 λ(δ)=A·e^(-kδ) 的 A/k、
        波动率 σ、最优半价差与库存 skew（论文式 4.6/4.7）、以及报价被打穿的概率。

        Args:
            arrival_depths: 市价单相对当刻 mid 的穿透深度（tick 数）。
                买方市价单 = 成交价tick - mid_tick；卖方市价单 = mid_tick - 成交价tick。
                快速行情中出现在 mid 另一侧的负深度会被自动剔除。建议至少数千笔。
            mid_price_chg: 每 step_seconds 的 mid 变化（tick 数，波动率估计用）。
            gamma: 风险厌恶系数（官方 0.05；越大报价越宽、越不敢持仓）
            delta: tick 步长（官方 1）
            step_seconds: 采样步长（秒；官方 100ms = 0.1）
            fit_depth_ticks: 交易强度只拟合距 mid 这么近的档位（官方 70；远端指数拟合失真）
            adj1: 半价差调节因子（官方 1）
            adj2: skew 调节因子（官方 0.05——裸模型 adj2=1 会因 skew 过强导致 SR=-246）
            position: 当前持仓（张数，演示库存 skew 如何偏移报价）
            mid_price_tick: 演示报价用的当前 mid（tick 数），默认 100000

        Returns:
            dict: A/k/σ、half_spread/skew、reservation/bid/ask、打穿概率、拟合诊断
        """
        if not arrival_depths:
            raise ValueError("arrival_depths 不能为空")
        if not mid_price_chg:
            raise ValueError("mid_price_chg 不能为空")
        return glft_mm.calibrate_glft(
            arrival_depths=arrival_depths,
            mid_price_chg=mid_price_chg,
            gamma=gamma,
            delta=delta,
            step_seconds=step_seconds,
            fit_depth_ticks=fit_depth_ticks,
            adj1=adj1,
            adj2=adj2,
            position=position,
            mid_price_tick=mid_price_tick,
        )

    @mcp.tool()
    def compute_microstructure_signals(
        bid_prices: list[list[float]],
        bid_qty: list[list[float]],
        ask_prices: list[list[float]],
        ask_qty: list[list[float]],
        depth_range: float = 0.025,
        tick_size: float = 1.0,
        std_window: int | None = None,
        ic_horizon: int | None = None,
    ) -> dict:
        """微观结构信号（hftbacktest Pricing Framework 复刻）：OBI / VAMP / Effective-VAMP。

        输入一组 L2 盘口快照（每快照为从 best 到深处的价格与数量列表），输出每个快照的
        订单簿不平衡、VAMP、Effective-VAMP、标准化 OBI（滚动 z-score）及 VAMP 收益率。

        Args:
            bid_prices / bid_qty: 每个快照的 bid 侧价格与数量（从 best 向下）
            ask_prices / ask_qty: 每个快照的 ask 侧价格与数量（从 best 向上）
            depth_range: 聚合深度范围（相对 mid 比例，0.025 = 2.5%；官方参考 0.0025~0.025）
            tick_size: 价格最小步长
            std_window: 标准化 OBI 的滚动窗口（快照数）；None = 全序列（官方 1h/0.1s = 36000）
            ic_horizon: 若提供（前向步数），额外计算各信号与未来 mid 收益的 IC 信息系数

        Returns:
            dict: 各信号的逐快照序列 + 滚动统计 + 可选的 IC 评估
        """
        res = microstructure.compute_signals_series(
            bid_prices, bid_qty, ask_prices, ask_qty,
            depth_range=depth_range, tick_size=tick_size, std_window=std_window,
        )
        if ic_horizon:
            res["ic"] = {
                "horizon_steps": ic_horizon,
                "std_obi": round(
                    microstructure.information_coefficient(
                        [0.0 if v is None else v for v in res["std_obi"]],
                        res["mids"], ic_horizon), 4),
                "vamp_eff_returns": round(
                    microstructure.information_coefficient(
                        [0.0 if v is None else v for v in res["vamp_eff_returns"]],
                        res["mids"], ic_horizon), 4),
                "note": "IC 越接近 ±1 越强；官方 BTC alpha 的 IC 峰值出现在 ~10 分钟前向。",
            }
        return res

    @mcp.tool()
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
        """延迟对做市收益的影响模拟（hftbacktest "Impact of Order Latency" 教程复刻）。

        同一行情、同一做市策略，仅改变订单生效延迟（步数），对比 PnL 与夏普。
        官方实测教训：ETHUSDT 5 天 GLFT 做市，feed 延迟回测 SR -0.20、真实延迟 +1.54、
        放大延迟 -0.38——延迟模型不同，同一策略从亏变赚。

        Args:
            n_steps: 模拟步数（每步 ~100ms 量级）
            sigma_per_step: mid 每步波动（tick）
            arrival_prob: 每步市价单到达概率
            info_strength: 信息流强度（>0 使市价单方向与近期收益耦合，制造逆向选择）
            half_spread_tick: 做市半价差（tick）
            latency_steps: 要对比的延迟配置（步数），默认 [0, 3, 10]
            rebate: 每笔成交返佣（0.00005 = 0.005% maker rebate；负数表示付费）
            seed: 随机种子（固定可复现）

        Returns:
            dict: 各延迟配置的 pnl/sr/fills + 定性结论 + 官方参照值
        """
        if n_steps < 100:
            raise ValueError("n_steps 过短（<100）")
        return latency_sim.simulate_latency_impact(
            n_steps=n_steps,
            sigma_per_step=sigma_per_step,
            arrival_prob=arrival_prob,
            info_strength=info_strength,
            half_spread_tick=half_spread_tick,
            latency_steps=latency_steps,
            rebate=rebate,
            seed=seed,
        )
