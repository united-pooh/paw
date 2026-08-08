# 08 · hftbacktest 系统学习：tick 级做市回测工具链拆解

> 资料源：https://hftbacktest.readthedocs.io/（Getting Started / Data Preparation / Probability Queue Models /
> GLFT Market Making / Market Making with Alpha - OBI / Impact of Order Latency / Level-3 Backtesting / Pricing Framework）
> 学习日期：2026-08-07。所有演示数值均来自官方教程实测结果。

## 0. 一句话总览

hftbacktest 是**以"成交仿真准确性"为信仰**的 HFT 回测框架：同时建模 **feed 延迟**（你看到什么）、
**order 延迟**（交易所何时处理你的单）与**队列位置**（你能排第几），再用真实 L2/L3 tick 数据重放。
它的核心观点：*回测既不能过度悲观（隐藏小 edge）也不能过度乐观（虚报收益），必须先能复现实盘，再谈优化与防过拟合。*

## 1. 数据层：事件归一化格式

所有原始 feed 统一转为 8 列事件数组：

```
ev | exch_ts | local_ts | px | qty | order_id | ival | fval
```

- `ev`：事件类型位掩码（低 8 位 + 高位标志）。关键标志：`BUY_EVENT=1<<29`、`SELL_EVENT=1<<30`、`ADD_EVENT=1<<31`、
  `TRADE_EVENT`、`DEPTH_EVENT` 等。例：`0xE0000001` 表示卖方深度更新。
- `exch_ts`：交易所撮合引擎时间戳；`local_ts`：本地收到时间。**两者差即 feed 延迟**。
- L2（Market-By-Price）事件：价格档位增减量；L3（Market-By-Order）事件：带 order_id 的单笔增/改/撤。
- Binance 原始 feed 转换流程：`binancefutures.convert()` 先做**延迟纠正**（统计 local_ts 与 exch_ts 的系统偏移），
  再做**事件顺序纠正**（按 exch_ts 与 local_ts 分别 mergesort）；Tardis.dev 数据用 `tardis.convert()`，**trade 文件放在 book 文件之前**输入，
  使"由成交引发的深度变化"在回测中先看到成交再看到盘口，更真实。
- 跨天回测需要 **EOD snapshot**（`create_last_snapshot` 从当日 feed 尾部重建次日初始盘口）。

**教训：时间戳有两套，事件顺序要按撮合时间而非接收时间排序；延迟不是噪声而是信息。**

## 2. 回测引擎：三个"现实性旋钮"

### 2.1 延迟模型（决定生死）
- `constant_latency(entry, response)`：固定下单延迟与回执延迟。
- `intp_order_latency(data)`：用实测延迟分布（如 1 小时真实 order latency npz）线性插值采样。
- 无真实延迟数据时，可用 feed 延迟放大（entry=4×feed、response=3×feed）作为替代。

**实测（ETHUSDT 2023-04-01~05，GLFT 做市，5 天）：**

| 延迟配置 | SR | 结论 |
|---|---|---|
| 由 feed 延迟生成 | **-0.20** | 亏 |
| 真实 order latency | **+1.54** | 赚 |
| 放大 feed 延迟 | **-0.38** | 亏 |

同一策略、同一行情，只因延迟模型不同就从"赚"变"亏"——**回测里没有延迟 = 凭空多出几毫秒 alpha**。

### 2.2 队列位置模型（决定成交概率）
订单进入盘口后排在队列哪个位置，决定了同价市价单打过来时你能不能吃到。
官方提供：`square_prob_queue_model`、`log_prob_queue_model2`、`power_prob_queue_model(p)`、`risk_adverse_queue_model`（L2 概率估计）；
L3 数据则用 `l3_fifo_queue_model`（真实队列，无模型误差）。
教程用 100 个资产组合对比三种概率模型，**5 分钟重采样 SR 差异显著**（图形可见，数值未给出）→ 必须用实盘数据校准队列模型。
L3 vs L2 对比（CME BTCM4）：同一网格策略，L3 精确队列 vs L2 概率估计，权益曲线有肉眼可见分歧——策略越依赖队列优势，模型误差越致命。

### 2.3 成交与费用
- `no_partial_fill_exchange` / `partial_fill_exchange`。
- 费用模型：`trading_value_fee_model(maker, taker)`、`trading_qty_fee_model`、`flat_per_trade_fee_model`；
  **负值 = 返佣**（教程全部用 -0.00005，即 Binance 最高做市返佣 0.005%）。
- `GTX`（post-only）防成交交叉；订单生命周期 `NONE→NEW→FILLED/CANCELED/EXPIRED`，`wait_order_response` 等回执。

**核心公式：做市利润 ≈ 价差捕获 + 返佣 - 库存风险 - 逆向选择。返佣不是小钱，教程中多轮结论是"利润主要来自返佣"。**

## 3. GLFT 做市模型（Guéant–Lehalle–Fernandez-Tapia，论文式 (4.6)/(4.7)）

### 3.1 交易强度（到达率）校准
假设市价单到达率随"距 mid 的 tick 距离 δ"指数衰减：

```
λ(δ) = A · e^(-k·δ)
```

校准步骤（教程原码）：
1. 每 100ms 记录一次"市价单穿透深度"：买方单 = 成交价tick − mid_tick，卖方单 = mid_tick − 成交价tick；
   同一时步取最大深度；快速行情中出现在 mid 另一侧的（负深度）直接剔除。
2. 10 分钟窗口内计数：`tick = round(depth / 0.5) - 1`，`out[:tick] += 1`（表示"若我挂在此距离内则被成交"）。
3. `λ(δ) = 计数 / 600`（10 分钟 → 每秒），对前 70 个 tick 做 `ln λ = -k·δ + ln A` 线性回归：
   `A = exp(截距)`，`k = -斜率`。
4. **只拟合浅层（70 tick）**：全范围拟合会高估近端、低估远端；报价本来就落在近端。
   实测（ETHUSDT 2022-10-03）：全范围 A=0.84, k=0.017 → 浅层重拟合 A=2.99, k=0.042。

### 3.2 波动率
```
σ = std(mid_tick 每 100ms 变化) × √10      # 换算成 tick/√秒
```
实测 σ ≈ 10.7 tick/√s。

### 3.3 最优报价（闭式解）
```
c1 = 1/(ξδ) · ln(1 + ξδ/k)
c2 = √( γ/(2Aδk) · (1 + ξδ/k)^(k/(ξδ)+1) )
half_spread = c1 + (δ/2)·c2·σ
skew        = c2·σ
reservation  = mid - skew · position          # 库存风控：持仓越大报价越偏
bid  = min(round(reservation − half_spread), best_bid)
ask  = max(round(reservation + half_spread), best_ask)
```
实测参数：γ=0.05（风险厌恶）、δ=1（tick）、ξ=γ → half_spread≈20.5 tick、skew≈9.8 tick（仅 2 手持仓就抵消整个半价差！）。

**打穿概率诊断**：统计历史到达深度中 > half_spread 的比例——ETHUSDT 示例只有 **1.86%** 的市价单能打到你（按笔数，非按量）。

### 3.4 实战校正：裸模型很烂
| 配置 | 结果 |
|---|---|
| 裸 GLFT（skew 过强不敢持仓） | 单日 SR **−246** |
| + adj2=0.05 弱化 skew | 单日 SR +1.20 |
| + 网格化（20 档，interval=round(half_spread)·tick） | 5 日 SR **+19.8** |
| 同样方法搬到 LTCUSDT | 5 日 SR +17.2 |

教训：**模型给方向，网格给容量，adj 因子是工程调参**；且 skew 太强 = 永远不持仓 = 永远不成交 = 亏损。

## 4. 微观结构信号（Pricing Framework）

### 4.1 信号族（全部以"距 mid 一定深度 N"聚合盘口）
设 d 为离 mid 的 tick 距离，ΣQ_bid/ΣQ_ask 为 N 深度内买卖挂单总量，Σd·Q 为加权和：

```
OBI（订单簿不平衡）     = ΣQ_bid − ΣQ_ask
标准化 OBI              = (OBI − mean_1h) / std_1h      # 教程窗口 1h
VAMP（量加权中点价）     = (ΣP_bid·Q_ask + ΣP_ask·Q_bid) / (ΣQ_bid + ΣQ_ask)   # 交叉乘！
bid_eff/ask_eff         = Σ(P·Q)/ΣQ（同侧加权均价）
Effective VAMP          = (bid_eff·Q_ask + ask_eff·Q_bid) / (ΣQ_bid + ΣQ_ask)
Weighted-Depth 价       = 同侧 Σ(P·Q)/ΣQ（N 定义为固定总量而非比例）
```

**关键技巧（Pricing Framework 原码）**：VAMP 可分解为
`tick_size × (mid_tick·ΣQ_ask − Σd·Q_ask + mid_tick·ΣQ_bid + Σd·Q_bid) / (ΣQ_bid + ΣQ_ask)`，
因此只需预计算 4 个累计量 `(ΣQ_bid, Σd·Q_bid, ΣQ_ask, Σd·Q_ask)`，任意深度范围即取即用——这就是官方 `precompute_obi` 的加速核心。

### 4.2 定价框架：alpha = 收益率表达
```
fair 价（收益形式）:
  rev_usdt  = spot_USDT 15m 累计收益 − futures 15m 累计收益     # 期货向现货回归
  rev_fdusd = spot_FDUSD 15m 累计收益 − futures 15m 累计收益
  rev_mkt   = 等权市场 5m 收益 − 本资产 5m 收益                  # 统计套利市场回归
  vamp_ret  = VAMP / mid − 1
  std_obi   = 标准化 OBI × 0.0001                               # 缩放对齐收益量纲
```
**主市场原理**：价格发现发生在最大成交量市场（Binance 上 FDUSD 现货零费率、量大于 USDT 现货），
小市场/小币种看自己的盘口没用，要看主市场的盘口与主资产的定价。BTC 的 alpha 模型直接迁移到 ETH/XRP/SOL/DOGE 反而更好（见 4.4）。

### 4.3 加速回测：预计算 + 简化状态机
- 预计算列：`local_ts / best_bid_tick / best_ask_tick / bid_fill_tick / ask_fill_tick / order_ack_ts /
  best_bid_ack / best_ask_ack / fill_ack / fill_after_ack`（100ms 网格）。
- 状态机：`req_*`（已发出未确认）与 `open_*`（已生效）；GTX 交叉即拒；ack 前先查旧单是否成交，ack 后再查一次；
  `INVALID_MIN/MAX` 表示无单。把"事件驱动"变成"网格驱动"，速度提升几个数量级。

### 4.4 官方实测数值（2025-08-01 单日，BTCUSDT 期货，半价差 0.025%，order=$50k）

| alpha | SR | 单笔收益 |
|---|---|---|
| 无 alpha（纯库存做市） | 7.6 | 0.0068% |
| rev_usdt（现货回归） | **28.5** | 0.0156% |
| rev_fdusd | 12.0 | 0.0142% |
| rev_mkt（单独） | **−21.3** | −0.0062% |
| rev_usdt + rev_fdusd | 23.2 | 0.0153% |
| VAMP 0.25% | **25.6** | 0.0179% |
| Eff-VAMP 0.5% | 18.1 | 0.0240% |
| 标准化 OBI 2.5% | 21.4 | 0.0298% |
| 等权四信号 | 24.2 | 0.0419% |
| 加权（0.3/0.4/0.15/0.15） | 32.2 | 0.0497% |
| 加权 + 0.1·rev_mkt | **49.9** | 0.0372% |

**跨资产迁移**（同一 BTC alpha 用在别币）：ETH SR 21.5、XRP SR 28.7、SOL SR 24.1、DOGE SR 19.8——
多数比各币自己的 alpha 更好（BTCUSDT 单独 SR 28.5 vs 用 BTC alpha 的 ETH 21.5 除外，但 XRP 28.7 > 自己的 13.2）。

**官方警告**：权重越调越好 = 过拟合警报；"单个 alpha 的 edge 有限且嘈杂，定价的实质是管理过拟合风险"。

## 5. OBI 做市的长周期衰减（2023→2025）

| 时期 | 资产 | 参数 | SR | 单笔收益 |
|---|---|---|---|---|
| 2023-05 | BTC | half_spread 80t, skew 3.5, c1 160, 2.5% 深度 | **10.8** | 0.0139% |
| 2023-05 | ETH | half_spread 5t, skew 0.2, c1 10 | 9.0 | 0.0118% |
| 2023-05 | BTC | 高频低距（0.1% 深度, 500ms, 冲返佣） | 14.0 | 0.0021% |
| 2025-02 | BTC | 同参数（队列模型 p2→p3 更保守） | 5.4 | 0.0086% |
| 2025-05~07 | BTC | 同参数 | **3.0** | 0.0044% |

结论：OBI 依然有效但 **edge 两年衰减 3.6 倍**（拥挤+微结构进化），返佣占比越来越大——做市生意本质是"卖流动性收租"，alpha 只是租金加成。

## 6. 工具链拆解 → 本 MCP 的转化

| hftbacktest 内部能力 | 本 MCP 工具 |
|---|---|
| `measure_trading_intensity` + `linear_regression` + `compute_coeff`（GLFT 4.6/4.7） | `calibrate_glft_mm` |
| `precompute_obi` + VAMP/EffVAMP/标准化 OBI + `ic()` 信息系数 | `compute_microstructure_signals` |
| Impact of Order Latency 教程（feed/order 延迟对比） | `simulate_latency_impact` |

设计原则：纯 numpy、无外部依赖；lib 层无 I/O（可单测），tools 层做参数校验与可读化输出；
每个工具的输出都附带"官方实测参照值"，防止脱离真实量纲。

## 7. 关键经验清单（可直接用于策略评审）

1. **延迟模型 = 生死**：同一策略 5 天，feed 延迟回测 SR −0.20，真实延迟 +1.54，放大延迟 −0.38。
2. **返佣是主粮**：BTC OBI 2025 单笔收益 0.0086%，其中 0.005% 是返佣；无返佣的做市回测基本不成立。
3. **skew 过强 = 不敢持仓 = 不成交 = 亏损**（裸 GLFT 单日 SR −246 的教训）。
4. **网格 = 容量**：GLFT 单档 SR +1.2 → 20 档网格 SR +19.8。
5. **只拟合浅层**：交易强度指数拟合只在近端（~70 tick）可靠。
6. **看主市场**：价格发现在主市场；小市场自己的 OBI 不如主市场/主资产的 OBI。
7. **信号衰减**：OBI 做市 SR 10.8→5.4→3.0（两年），任何 micro-alpha 都要按年复查。
8. **组合权重的诱惑 = 过拟合**：官方自己演示了 49.9 的高 SR，同时明说"需要更长验证期判断真伪"。
9. **L2 队列模型必须用实盘校准**；有 L3 用 L3。
10. **回测第一原则**：先让回测复现实盘（同一策略、同一月份），再谈优化与防过拟合。
