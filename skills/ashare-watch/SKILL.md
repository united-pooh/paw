---
name: ashare-watch
description: A股量化盯盘工作流 — 盘前（数据路由选源+拉行情+活跃计划检查）→ 盘中（分钟级信号/异动监控/模拟盘下单）→ 盘后（日终结算+交易记忆+复盘）。触发词：盯盘、看盘、监控、watch、行情快照。
source: 综合 HKUDS/Vibe-Trading + skillhub trading-memory（MIT/吸收）
---

# A股量化盯盘

个人量化盯盘的完整工作流。数据全部走免费/低门槛源（akshare / baostock / tushare），
模拟交易走 SQLite 模拟盘（quant-mcp `paper_*` 工具），记忆走 trading-memory 协议。

## 数据源路由（先选源，再取数）

| 优先级 | 数据源 | 认证 | 适用 |
|--------|--------|------|------|
| 1 | akshare | 无 | A股日线/分钟/实时/基本面，免费免注册，首选 |
| 2 | baostock | 无（登录匿名） | A股日线/分钟，稳定，回退用 |
| 3 | tushare | `TUSHARE_TOKEN` | 数据质量最好；分钟级需积分≥2000 |

- 有 token 时优先 tushare，无 token 时 akshare → baostock 自动回退
- 东财系接口（akshare 的 `*_em` 系列）有 IP 限流，连续调用要加间隔，别猛锤
- 关键数字跨 ≥2 个独立源交叉验证；偏差 >1% 标 ⚠️

## 盯盘三时段

### 盘前（9:00-9:25）
1. 拉自选股最新日线：`ak.stock_zh_a_hist(symbol, period="daily", adjust="qfq")`
2. 检查昨日异动：涨跌幅/成交量/换手率排序，标注突破或放量
3. 运行 `check_active_plans`（trading-memory）：当前行情是否命中活跃交易计划
4. 查 `get_agent_state`：确认不在 tilt / 连亏状态
5. 检查模拟盘账户可用资金与 T+1 可卖持仓（`paper_account_summary`）

### 盘中（9:30-15:00）
1. 实时快照：`ak.stock_zh_a_spot_em()` 拉全市场或自选快照（价格/涨跌幅/量比/换手/主力净流入）
2. 分钟级盯盘：`ak.stock_zh_a_hist_min_em(symbol, period="5")`，计算 VWAP/TWAP：
   ```python
   typical = (high + low + close) / 3
   vwap = (typical * vol).cumsum() / vol.cumsum()   # 价格在 VWAP 上/下 → 日内强弱
   ```
3. 异动规则（示例）：涨幅 > 5% 且量比 > 2 → 提醒；跌破 VWAP 且持仓 → 提醒风险
4. 信号触发 → `paper_submit_order` 模拟下单（虚拟资金，不影响真实账户）
5. 涨跌停价校验：`ak.stock_zh_a_hist` 当日涨跌幅 ≥ 9.9% 时不再追高（主板）

### 盘后（15:00 后）
1. `paper_daily_settle`：按收盘价日终结算，生成净值快照（含未实现盈亏）
2. `remember_trade`：每笔平仓写 trading-memory（**结果未知前记录信心**）
3. `get_behavioral_analysis`：处置效应、持仓时长、连亏检查
4. 复盘输出：今日交易 + 净值曲线 + 明日计划（写 Prospective memory）

## 模拟盘规则（quant-mcp paper_* 工具）

- 每笔委托记录：时间/标的/方向/价格/数量/状态（已报/已成/已撤/废单）
- A股约束：T+1（当日买入次日才能卖）、100 股整数倍、佣金万 2.5（最低 5 元）+ 卖出印花税 0.05%
- 成交价默认按委托价（limit）或最新价（market，用当前快照价）
- 资金不足/持仓不足 → 废单并返回原因，绝不穿仓
- 日终按收盘价 mark-to-market，未实现盈亏计入净值

## 红线

- 模拟盘永不连接真实下单通道（shadow-account 原则：不落单）
- 盯盘输出不是投资建议；所有"信号"仅研究用
- 数据源失败必须回退或明说失败，不许编造行情
- tushare token 走环境变量，不进代码
