# quant-mcp：AI 量化交易 MCP 服务器

把《AI量化交易从0到1》（Wayland Zhang）学习成果 + hftbacktest 方法论封装为 MCP 工具：
**反过拟合 / 最优执行 / Regime 检测 / 死亡方式体检 / 多智能体模拟 / HFT 做市工具箱 /
行情数据 / SQLite 模拟盘** + 学习笔记资源。

核心算法手写（Baum-Welch、Acklam 分位数、蒙特卡洛、GLFT 闭式解）；
数据层封装 akshare / baostock / tushare 三源自动回退（免费源，供盯盘与研究）。

## 能力清单

### Tools（22 个）

**分析工具（原 9 个）**

| 工具 | 说明 | 来源 |
|------|------|------|
| `calc_deflated_sharpe` | DSR 显著性检验：这个回测夏普是运气吗？ | López de Prado 2014, JPM |
| `calc_min_sharpe` | 反推最小合格夏普（试过 N 个组合后） | 同上 |
| `calc_ac_execution` | Almgren-Chriss 最优执行成本-风险分析 | AC 2000 / 书第19课 |
| `detect_regime_hmm` | 高斯 HMM 市场状态识别（手写 Baum-Welch+Viterbi） | 书第12课 |
| `autopsy_strategy` | 12 种死亡方式完整体检（复用 DSR） | 书附录B |
| `simulate_multi_agent` | 多智能体交易系统模拟（一票否决对照） | 书第11课 |
| `calibrate_glft_mm` | GLFT 做市校准：A/k + σ + 最优半价差/skew + 打穿概率 | hftbacktest / GLFT 论文 |
| `compute_microstructure_signals` | OBI / VAMP / Effective-VAMP + IC | hftbacktest Pricing Framework |
| `simulate_latency_impact` | 延迟对做市 PnL 的影响对比 | hftbacktest Impact of Order Latency |

**数据工具（4 个，akshare/baostock/tushare 自动回退）**

| 工具 | 说明 |
|------|------|
| `fetch_kline` | A 股 K 线（日线/1/5/15/30/60 分钟，qfq/hfq/none），源失败自动回退 |
| `fetch_realtime_quotes` | 实时行情快照（价格/涨跌幅/量比/换手/PE/PB），盯盘用 |
| `fetch_stock_list` | A 股全部上市股票列表 |
| `watch_snapshot` | 盯盘快照：自选股实时行情 + 20 日均线偏离度趋势摘要 |

**模拟盘工具（9 个，SQLite，永不连接真实下单）**

| 工具 | 说明 |
|------|------|
| `create_paper_account` | 创建虚拟资金账户 |
| `paper_submit_order` | 下单（限价/市价，立即成交或挂单；T+1、100 股整手、佣金万 2.5、印花税） |
| `paper_cancel_order` | 撤单 |
| `paper_fill_pending` | 按最新价撮合挂单（价格触发才成交） |
| `paper_account_summary` | 账户汇总：现金/市值/总资产/浮动盈亏 |
| `paper_positions` | 持仓明细（含 T+1 冻结） |
| `paper_orders` | 委托记录查询 |
| `paper_daily_settle` | 日终结算：重估→净值快照→T+1 解冻 |
| `paper_equity_curve` / `paper_close_account` | 净值曲线 / 关闭账户 |

### Resources（5 个）
- `quant://notes/papers-map` — 论文勘误地图（书里 6/15 个 arXiv 链接是错的）
- `quant://notes/death-modes` — 12 种死亡方式速查 + 排查顺序
- `quant://notes/reading-path` — 四步学习路线图
- `quant://notes/hft-toolbox` — HFT 资料库速查（四基石论文/工具/宏观逻辑）
- `quant://notes/hftbacktest-playbook` — 做市方法论速查（GLFT 公式/三旋钮/实测数值）

### Prompts（2 个）
- `review_strategy` — 引导 AI 对用户策略做 12 项体检
- `read_papers` — 论文阅读引导（勘误版）

## 安装

```bash
cd quant-mcp
pip install -e .              # 含 akshare + baostock
pip install -e ".[tushare]"   # 可选：tushare（需 TUSHARE_TOKEN 环境变量）
```

## 运行与测试

```bash
# stdio（默认，供 Claude Desktop / Cursor 等客户端）
python -m quant_mcp

# SSE 模式（端口 8899）
python -m quant_mcp --transport sse

# 集成测试（InMemoryTransport 全链路：24 工具 + 5 资源 + 2 提示词）
PYTHONPATH=src python -m pytest tests/ -v
```

已通过：论文数值例子复现（N=100 → DSR=0.8997；N=46 → 0.9505）、
AC 成本-风险单调性、HMM 状态识别、体检健康度、多智能体风控对照、
GLFT 校准数值性质、VAMP 价格区间、延迟-成交单调性、模拟盘 T+1/费用/撮合、
数据源回退与标准化、stdio 真实握手（39 tests）。

## 模拟盘使用示例

```
1. 创建账户：create_paper_account(name="我的模拟盘", initial_cash=1000000)
2. 看行情：   fetch_realtime_quotes(symbols=["000001", "600519"])
3. 买入：     paper_submit_order(account_id, "000001", "buy", 1000, price=10.5)
              # 当日买入 1000 股，T+1 冻结不可卖
4. 挂单：     paper_submit_order(account_id, "600519", "buy", 100, price=1400, auto_fill=False)
              paper_fill_pending(account_id, {"600519": 1395})  # 价格到位才成交
5. 收盘结算： paper_daily_settle(account_id, price_map={"000001": 10.8})
6. 看曲线：   paper_equity_curve(account_id)
```

模拟盘数据库默认在 `~/.quant-mcp/paper_trading.db`（可用 `QUANT_MCP_DB_DIR` 改目录）。

## 客户端配置

**Claude Desktop**（`claude_desktop_config.json`）：

```json
{
  "mcpServers": {
    "quant-mcp": {
      "command": "/opt/miniconda3/envs/astrbot/bin/python",
      "args": ["-m", "quant_mcp"],
      "cwd": "/Users/united_pooh/python project/go-code/quant-mcp"
    }
  }
}
```

**Cursor / 其他 MCP 客户端**：

```json
{
  "mcpServers": {
    "quant-mcp": {
      "command": "/opt/miniconda3/envs/astrbot/bin/python",
      "args": ["-m", "quant_mcp"],
      "env": { "PYTHONPATH": "/Users/united_pooh/python project/go-code/quant-mcp/src" }
    }
  }
}
```

> 提示：建议 `pip install -e .` 安装后，命令可简化为 `python -m quant_mcp`（无需 PYTHONPATH）。

## 使用示例（给 AI 的提示词）

```
帮我体检一下我的策略：日收益序列 [0.001, -0.002, ...]（756 天），
回测时试过 200 个参数组合，滑点 5bp，日均换手 50%。
→ 应调用 autopsy_strategy，若 DSR 不过再用 calc_min_sharpe 解释。
```

```
我的回测年化夏普 3.2，试了 80 个参数，样本 3 年，这个夏普可信吗？
→ calc_deflated_sharpe(annualized_sharpe=3.2, n_trials=80, sample_days=756)
```

```
我采集了市价单穿透深度序列和 mid 变化序列，想做市，报价该挂多远？
→ calibrate_glft_mm(arrival_depths=..., mid_price_chg=...)
   —— 输出最优半价差/skew 和"报价被打穿的概率"（官方 ETH 示例仅 1.86%）

给我这个盘口快照算 OBI / VAMP / Effective-VAMP 信号
→ compute_microstructure_signals(bid_prices=[[...]], bid_qty=[[...]], ...)

做市策略在延迟 0ms vs 10ms 下会差多少？
→ simulate_latency_impact(n_steps=4000, latency_steps=[0, 3, 10])
```

## 项目结构

```
quant-mcp/
├── pyproject.toml            # mcp>=1.26 + numpy + pandas + akshare + baostock（tushare 可选）
├── src/quant_mcp/
│   ├── server.py             # FastMCP 实例组装（build()）
│   ├── __main__.py           # python -m quant_mcp 入口
│   ├── lib/                  # 纯算法层（无 I/O，可单测）
│   │   ├── stats.py          # norm_cdf/ppf(Acklam)、夏普、偏度峰度、回撤
│   │   ├── deflated_sharpe.py
│   │   ├── execution.py      # Almgren-Chriss
│   │   ├── hmm.py            # 手写 Baum-Welch + Viterbi
│   │   ├── autopsy.py        # 12 种死亡方式
│   │   ├── multi_agent.py    # 多智能体模拟
│   │   ├── glft_mm.py        # GLFT 做市校准（A/k/σ → 最优报价）
│   │   ├── microstructure.py # OBI/VAMP/EffVAMP/Weighted-Depth 信号 + IC
│   │   ├── latency_sim.py    # 延迟/信息流影响的做市模拟
│   │   ├── datasource.py     # akshare/baostock/tushare 统一封装 + 自动回退
│   │   └── paper_sim.py      # SQLite 模拟盘引擎（T+1/费用/撮合/日终结算）
│   ├── tools/                # 22 个工具（register(mcp) 模式）
│   ├── resources/notes.py    # 学习笔记资源（5 个）
│   └── prompts/strategy_review.py
└── tests/                    # 39 个测试（分析 15 + 数据 9 + 模拟盘 15）
```

## 学习方法论（沉淀自八道菜）

1. **任何夏普都要回答 5 个问题**：试验次数 N、各试验夏普方差、样本长度 T、偏度、峰度——不披露试验次数的回测报告 = 耍流氓（DSR）。
2. **显示价 ≠ 成交价**：没有免费午餐，只有用 Almgren-Chriss 框架可选择的代价。
3. **Regime 检测的价值 = 识别准确性 × 策略差异 − 切换成本**；过渡期宁可模糊，不可硬切（HMM 软切换）。
4. **别当"全能 Agent"**：分工 + 一票否决是资金最后防线（Knight Capital 45 分钟亏 4.4 亿美元的教训）。
5. **先体检，再上路**：12 种死亡方式，数据→Regime→执行→风控→流动性→相关性→杠杆→人工→系统→监管→对手盘→过拟合。
6. **arXiv 链接错误率高**：书中 15 个链接 6 个张冠李戴，引用前逐个核对。
7. **回测里没有延迟 = 凭空多出几毫秒 alpha**：同一策略 ETHUSDT 5 天，feed 延迟 SR −0.20 vs 真实延迟 +1.54（hftbacktest 官方实测）。
8. **做市利润 = 价差 + 返佣 − 库存 − 逆向选择**：返佣是主粮（OBI 单笔收益 0.0044% 里 0.005% 是返佣）；skew 过强 = 不持仓 = 亏损（裸 GLFT SR −246）。
9. **micro-alpha 按年复查**：OBI 做市 SR 10.8（2023）→ 5.4（2025-02）→ 3.0（2025-05）；组合权重越调越好 = 过拟合警报。
