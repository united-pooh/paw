"""学习笔记 Resources：论文勘误地图、12 种死亡方式速查、学习路线图、HFT 资料库。"""

from mcp.server.fastmcp import FastMCP

PAPERS_MAP = """# 论文精读地图（《AI量化交易从0到1》ArXiv 论文清单 · 已逐篇勘误）

> 全部 15 个 arXiv 链接已逐篇打开核验：**6 个编号错误（40%）**，以下为修正版。

## 勘误表（❌ 书里编号 → ✅ 正确论文）
- FinRL → `arXiv:2011.09607`（书里给 2007.14592 是无人机 SLAM）
- RL 金融综述 → `arXiv:2112.04553`（书里给 2109.04946 是弦论弱引力猜想）
- 多智能体股票交易 → `arXiv:2412.20138`（书里给 2103.03705 是脑部影像）
- Deep LOB 预测 → `arXiv:2403.09267` / DeepLOB `1808.03668`（书里给 2107.00934 是病理切片）
- 风险敏感 RL → 书里给 2006.05817 是自动驾驶论文
- FinMem → `arXiv:2311.13743`（书里给 2312.11274 是教育辅导论文）

## 按主题的精读优先级
| 主题 | 必读 | 为什么 |
|------|------|--------|
| 反过拟合 | Pseudo-Mathematics / Deflated Sharpe / AFML | 不读这三篇，其它论文结果都不可信 |
| 因子 ML | Gu-Kelly-Xiu (NBER w25398) | 收益来自波动率择时与分散化 |
| 执行 | Almgren-Chriss 2000 + `1906.11046` | 显示价 ≠ 成交价 |
| RL | FinRL `2011.09607` | 工程范式：环境/代理/应用/基准 |
| 多智能体 | TradingGPT `2309.03736` / FinMem `2311.13743` | 分层记忆 + 辩论 + 盈亏驱动记忆 |
| LLM 应用 | MarketSenseAI `2401.03737` / FinAgent `2402.18485` | 已上期刊的 LLM 选股实证 |

## 铁律
1. 任何策略报告必须披露：试验次数 N、各试验夏普分布、样本长度 T、偏度峰度。
2. 回测不是验证，OOS + 实盘小仓位才是。
3. arXiv 链接错误率高，引用前逐个核对。
"""

DEATH_MODES = """# 12 种死亡方式速查（附录B）

| # | 死亡方式 | 典型症状 | 处方 |
|---|---------|---------|------|
| 1 | 数据污染 | 极端跳变/零收益/除权错标 | 多源交叉验证 + 变更告警 |
| 2 | 过拟合 | 回测夏普>3 实盘<0.5 | 严格 OOS + 限制参数（用 calc_deflated_sharpe） |
| 3 | Regime 漂移 | 策略失效于市场状态变化 | Regime 检测（用 detect_regime_hmm） |
| 4 | 执行失真 | 滑点远超预期 | 保守成本假设（用 calc_ac_execution） |
| 5 | 风控失效 | 熔断不响应（Knight Capital 4.4亿美元/45分钟） | 风控独立 + 不可绕过 |
| 6 | 流动性枯竭 | 止损单无法成交（2015-08-24 闪崩） | 监控盘口深度 |
| 7 | 相关性飙升 | 分散化失效（LTCM 0.2→0.95） | 危机相关性压力测试 |
| 8 | 杠杆爆仓 | 强平归零（Archegos 200亿美元） | 杠杆<2x + 波动率调整 |
| 9 | 人为干预 | 手动取消止损 | 干预留痕 + 胜率<50% 禁干预 |
| 10 | 系统故障 | 订单发送失败（Facebook IPO） | 高可用 + 安全模式只平仓 |
| 11 | 监管变化 | 交易被禁/税改 | 分散地区 + 关注动态 |
| 12 | 对手盘适应 | Alpha 持续衰减 | 分散信号 + 持续研发 |

## 排查顺序（综合诊断表）
数据→Regime→执行→风控→流动性→相关性→杠杆→人工→系统→监管→对手盘→过拟合

## 每周健康检查
12 项清单（数据质量/Regime/滑点/风控/流动性/相关性/杠杆/人工/系统/监管/衰减/OOS）——用 autopsy_strategy 工具自动跑前 8 项。
"""

READING_PATH = """# 学习路线图（四步）

1. **反过拟合地基（1周）**：Pseudo-Math → Deflated Sharpe → AFML 11-12章 → 跑 calc_deflated_sharpe
2. **经典量化（1-2周）**：Gu-Kelly-Xiu → Almgren-Chriss → Market Making via RL → Deep Portfolio Theory
3. **RL/多智能体（2-4周）**：FinRL 跑通 → Multi-Agent Liquidation → TradingGPT/FinMem 架构复述
4. **LLM 应用（持续）**：Survey 选型 → FinGPT 微调 → MarketSenseAI/FinAgent 实证

工具配套：calc_deflated_sharpe(2) → calc_ac_execution(3) → simulate_multi_agent(4) → detect_regime_hmm(3) → autopsy_strategy(全程)。
"""


HFT_TOOLBOX = """# HFT 资料库速查（完整版见 quant-study/07_高频交易与工具资料库.md）

## 论文（HFT 四基石）
- Almgren-Chriss 2000 最优执行（本服务器 calc_ac_execution 已实现）
- Avellaneda-Stoikov 2008 做市定价（库存风险 vs 价差收益）
- Kearns & Nevmyvaka 2013 ML for Market Microstructure（综述）
- Cartea-Jaimungal-Penalva《Algorithmic and HFT》（随机控制教材）

## 工具
- **hftbacktest**（4.3k⭐，Python+Numba/Rust）：tick-by-tick 回测，L2/L3 全订单簿重建、延迟模型、队列位置成交模拟，可接 Binance/Bybit 实盘
- Hummingbot（做市机器人）、PandoraTrader（C++ 高频）、tbt（Rust）
- 中低频：Backtrader / Qlib / QuantConnect / vectorbt
- 数学：numpy/scipy/statsmodels（协整）/QuantLib/PyPortfolioOpt/arch/pykalman
- 微观结构信号：Order Book Imbalance、Micro-price、VAMP、VPIN、吸收率

## 政治经济逻辑（经典）
- **Dalio《How the Economic Machine Works》**：生产率 + 短债周期 + 长债周期（75-100年）
- **Minsky FIH**：繁荣内生不稳定（对冲→投机→庞氏），稳定孕育危机
- **Krishnamurthy & Lustig 2019**：美元=全球安全资产清算价，Treasury basis 走阔→美元升值
- **MSCI 地缘冲击实证**（5 次冲突）：中东式冲击 1 个月内修复；只有传导到"供给→通胀→央行"才变宏观冲击；2022 后股债对冲失效，黄金+美元成可靠分散器
- **Graham Capital**：宏观 alpha ∝ 央行利率变动幅度 × G10 政策分化度

## 铁律（HFT 版）
1. 回测必须精确模拟延迟/队列/返佣，否则先别谈过拟合（hftbacktest 原话）
2. OBI 做市 alpha 在衰减：SR 10.8（2023）→ 5.4（2025-02）→ 3.0（2025-05）
3. 地缘事件看传导链，不看新闻本身
"""


HFTBACKTEST_PLAYBOOK = """# hftbacktest 做市方法论速查（完整版见 quant-study/08_hftbacktest系统学习.md）

## 数据层
- 事件 8 列：ev | exch_ts | local_ts | px | qty | order_id | ival | fval
- **两套时间戳**：exch_ts=撮合时间、local_ts=本地收到时间，差即 feed 延迟；事件排序按撮合时间
- L2=价格档增减量，L3=带 order_id 的单笔增/改/撤；L3→L2 按档位聚合
- 跨天回测需 EOD snapshot 作初始盘口

## 三个现实性旋钮
1. **延迟模型**：constant_latency / intp_order_latency（实测延迟分布插值）。实测（ETHUSDT 5 天 GLFT）：
   feed 延迟回测 SR **-0.20**、真实延迟 **+1.54**、放大延迟 **-0.38** —— 延迟决定生死
2. **队列位置模型**：power_prob_queue_model(p)/risk_adverse（L2 概率估计）；l3_fifo（L3 真实队列）。
   必须用实盘数据校准；策略越依赖队列优势，模型误差越致命
3. **费用**：trading_value_fee_model(maker, taker)，**负值=返佣**（官方 -0.00005=0.005%）；返佣是做市主粮

## GLFT 做市模型（Guéant-Lehalle-Fernandez-Tapia，式 4.6/4.7）
- 交易强度 λ(δ) = A·e^(-k·δ)；校准：市价单穿透深度计数 → 前 70 tick 做 ln 线性回归
- σ = std(mid 每 100ms 变化) × √10（tick/√s）
- c1 = 1/(ξδ)·ln(1+ξδ/k)；c2 = √(γ/(2Aδk)·(1+ξδ/k)^(k/(ξδ)+1))
- half_spread = c1 + δ/2·c2·σ；skew = c2·σ；reservation = mid − skew·position
- 实测：half_spread≈20.5t、skew≈9.8t（仅 2 手抵消半价差）；打穿概率 1.86%
- **裸模型 SR=-246 → adj2=0.05 后 +1.2 → 20 档网格 +19.8**（skew 过强=不持仓=亏损）

## 微观结构信号（Pricing Framework）
- OBI = ΣQ_bid − ΣQ_ask（N 深度内）；标准化 OBI = 1h z-score × 0.0001
- VAMP = (ΣP_bid·Q_ask + ΣP_ask·Q_bid)/(ΣQ_bid+ΣQ_ask)（交叉乘）
- EffVAMP = (bid_eff·Q_ask + ask_eff·Q_bid)/(ΣQ_bid+ΣQ_ask)
- 加速核心：只累计 (ΣQ_bid, Σd·Q_bid, ΣQ_ask, Σd·Q_ask) 四个量，任意深度即取即用
- 主市场原理：价格发现在主市场；小市场看主市场的 OBI，小币种看 BTC 的 alpha

## 官方实测数值（2025-08-01 BTCUSDT 单日，纯做市 SR 7.6 基准）
- rev_usdt（现货回归）SR 28.5 | VAMP 0.25% SR 25.6 | std_obi SR 21.4 | 组合加权 SR 32~50
- **BTC alpha 迁移**：用在 ETH 21.5 / XRP 28.7 / SOL 24.1 / DOGE 19.8，多数优于各币自己的 alpha
- 权重越调越好 = 过拟合警报（官方原话）

## OBI 衰减（拥挤度体检）
SR：10.8（2023-05）→ 5.4（2025-02）→ 3.0（2025-05~07）；单笔收益 0.0139%→0.0044%，返佣占比上升

## 回测铁律
1. 先让回测复现实盘（同策略同月份），再谈优化与防过拟合
2. 回测里没有延迟 = 凭空多出几毫秒 alpha
3. 信号要按年复查衰减；组合权重是过拟合高发区
"""


def register(mcp: FastMCP) -> None:
    @mcp.resource("quant://notes/papers-map")
    def papers_map() -> str:
        """论文精读地图：勘误后的论文清单 + 按主题优先级。"""
        return PAPERS_MAP

    @mcp.resource("quant://notes/death-modes")
    def death_modes() -> str:
        """12 种死亡方式速查表 + 排查顺序 + 每周健康检查。"""
        return DEATH_MODES

    @mcp.resource("quant://notes/reading-path")
    def reading_path() -> str:
        """从 0 到 1 的四步学习路线图。"""
        return READING_PATH

    @mcp.resource("quant://notes/hft-toolbox")
    def hft_toolbox() -> str:
        """HFT 资料库速查：四基石论文、开源工具、政治经济逻辑、铁律。"""
        return HFT_TOOLBOX

    @mcp.resource("quant://notes/hftbacktest-playbook")
    def hftbacktest_playbook() -> str:
        """hftbacktest 做市方法论速查：GLFT 公式、微观结构信号、延迟/队列/返佣三旋钮、实测数值。"""
        return HFTBACKTEST_PLAYBOOK
