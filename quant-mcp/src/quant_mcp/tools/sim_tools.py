"""模拟盘工具：SQLite 驱动的虚拟资金交易（A股规则：T+1 / 整手 / 费用）。"""

from mcp.server.fastmcp import FastMCP

from ..lib import paper_sim


def register(mcp: FastMCP) -> None:
    @mcp.tool()
    def create_paper_account(
        name: str = "模拟盘",
        initial_cash: float = 1_000_000.0,
    ) -> dict:
        """创建模拟盘账户（虚拟资金，不连接任何真实下单通道）。

        Args:
            name: 账户名
            initial_cash: 初始虚拟资金，默认 100 万

        Returns:
            dict: account_id（后续所有 paper_* 工具都要传它）
        """
        return paper_sim.create_account(name=name, initial_cash=initial_cash)

    @mcp.tool()
    def paper_submit_order(
        account_id: str,
        symbol: str,
        direction: str,
        qty: int,
        price: float | None = None,
        order_type: str = "limit",
        auto_fill: bool = True,
        trade_date: str | None = None,
    ) -> dict:
        """模拟盘下单（A股规则：T+1、100 股整数倍、佣金万 2.5 + 卖出印花税）。

        Args:
            account_id: create_paper_account 返回的账户 ID
            symbol: 股票代码（000001）
            direction: buy / sell
            qty: 数量（A股需 100 的整数倍）
            price: 委托价。limit 单必填；market 单留空则自动拉实时价
            order_type: limit（限价）/ market（市价）
            auto_fill: True=立即按委托价成交；False=挂单，稍后用
                       paper_fill_pending 撮合
            trade_date: 交易日（默认今天）。用于 T+1 判断：当日买入不可卖

        Returns:
            dict: 委托记录；status=filled 表示已成交，rejected 附废单原因
        """
        return paper_sim.submit_order(
            account_id, symbol, direction, qty, price=price,
            order_type=order_type, trade_date=trade_date, auto_fill=auto_fill,
        )

    @mcp.tool()
    def paper_cancel_order(account_id: str, order_id: int) -> dict:
        """撤销模拟盘挂单（仅 pending 状态可撤）。"""
        return paper_sim.cancel_order(order_id)

    @mcp.tool()
    def paper_fill_pending(
        account_id: str,
        price_map: dict[str, float],
        trade_date: str | None = None,
    ) -> dict:
        """撮合模拟盘挂单：买单价 ≥ 最新价 或 卖单价 ≤ 最新价 才成交。

        用于模拟"挂了限价单后价格到位"的场景。未触发的单保留 pending。

        Args:
            account_id: 账户 ID
            price_map: {代码: 最新价}，如 {"000001": 12.5}
            trade_date: 交易日（默认今天）
        """
        return paper_sim.fill_pending_orders(
            account_id, price_map, trade_date=trade_date
        )

    @mcp.tool()
    def paper_account_summary(
        account_id: str,
        price_map: dict[str, float] | None = None,
    ) -> dict:
        """模拟盘账户汇总：现金 / 持仓市值 / 总资产 / 浮动盈亏。

        Args:
            account_id: 账户 ID
            price_map: 可选 {代码: 最新价}，提供则按此重估持仓
        """
        if price_map:
            return paper_sim.mark_to_market(account_id, price_map)
        return paper_sim.get_account_summary(account_id)

    @mcp.tool()
    def paper_positions(account_id: str) -> dict:
        """查询模拟盘持仓明细（含可卖数量/T+1 冻结）。"""
        return paper_sim.get_positions(account_id)

    @mcp.tool()
    def paper_orders(
        account_id: str,
        status: str | None = None,
        limit: int = 50,
    ) -> dict:
        """查询模拟盘委托记录。

        Args:
            account_id: 账户 ID
            status: 筛选状态 pending/filled/cancelled/rejected；None=全部
            limit: 返回条数上限
        """
        return paper_sim.get_orders(account_id, status=status, limit=limit)

    @mcp.tool()
    def paper_daily_settle(
        account_id: str,
        price_map: dict[str, float] | None = None,
        trade_date: str | None = None,
    ) -> dict:
        """模拟盘日终结算：按收盘价重估 → 写净值快照 → 解冻 T+1 仓位。

        收盘后调用。之后可用 paper_equity_curve 看净值曲线。

        Args:
            account_id: 账户 ID
            price_map: 可选 {代码: 收盘价}；不传则尝试拉实时行情
            trade_date: 结算日（默认今天）
        """
        return paper_sim.daily_settle(
            account_id, price_map=price_map, trade_date=trade_date
        )

    @mcp.tool()
    def paper_equity_curve(account_id: str) -> dict:
        """模拟盘净值曲线（每次 paper_daily_settle 生成一个点）。"""
        return paper_sim.get_equity_curve(account_id)

    @mcp.tool()
    def paper_close_account(account_id: str) -> dict:
        """关闭模拟盘账户（保留历史数据，无法再交易）。"""
        return paper_sim.close_account(account_id)
