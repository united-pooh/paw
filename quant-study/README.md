# quant-study：八道菜 🍽️

> 学习来源：[Wayland Zhang《AI量化交易：从0到1》→ ArXiv 论文推荐](https://waylandz.com/quant-book/ArXiv%20Papers/)
> 产出：2026-08-07（第1-2轮：论文清单 / 反过拟合 / 执行 / 多智能体；第3轮：Regime 检测 / 死亡方式；第4轮：HFT 资料库；第5轮：hftbacktest 系统学习）

## 菜一（冷盘）：论文清单勘误 + 精读地图 📋

`01_论文清单勘误与精读地图.md`

逐篇打开并核对了书里全部 15 个 arXiv 链接（比对标题与摘要），**发现 6 个编号是错的**（40% 错误率，张冠李戴到医学影像/无人机/弦论等无关领域），并给出正确的替代论文。同时按 6 大主题写了精读要点 + 四步学习路线图。

- ✅ 正确的 9 篇（FinAgent、TradingGPT、MarketSenseAI、FinGPT、BloombergGPT、Survey、Deep Portfolio Theory、Market Making via RL、Multi-Agent Liquidation、RL Framework、Stock Prediction）
- ❌ 错误的 6 篇 → 正确替代：
  - FinRL → `arXiv:2011.09607`（书里给的 2007.14592 是无人机 SLAM）
  - RL 金融综述 → `arXiv:2112.04553`（书里给的 2109.04946 是弦论弱引力猜想）
  - 多智能体股票交易 → `arXiv:2412.20138`（书里给的 2103.03705 是脑部影像）
  - Deep LOB 预测 → `arXiv:2403.09267` / DeepLOB `1808.03668`（书里给的 2107.00934 是病理切片）
  - 风险敏感 RL → 书里给的 2006.05817 是自动驾驶论文
  - FinMem → `arXiv:2311.13743`（书里给的 2312.11274 是教育辅导论文）

## 菜二（主菜）：Deflated Sharpe Ratio 反过拟合工具箱 🍳

`02_反过拟合工具箱/deflated_sharpe.py`（运行输出见 `output.txt`）

López de Prado《The Deflated Sharpe Ratio》(JPM 2014) 的**完整可运行实现**，纯 numpy + math（无 scipy），Python ≥ 3.8：

```bash
cd quant-study/02_反过拟合工具箱
python3 deflated_sharpe.py
```

包含 4 个演示：

1. **公式验证**：E[max SR] 解析公式 vs 蒙特卡洛模拟，误差 < 0.03（论文附录 A.2 实验复刻）
2. **论文例子复现**：N=100 → DSR=0.8997，与论文原值完全一致（实现正确性背书）
3. **随机策略池演示**：100 个真实夏普为 0 的噪声策略，冠军年化夏普 1.06，如实披露 N=100 时 DSR=0.44（不显著）；假装只试 1 次则 DSR=0.99（显著）——**同一策略，披露与否，命运天壤之别**
4. **最小合格夏普**：试 100 次要 3.3+ 的年化夏普才能过 95% 检验；试 1 万次要 4.6+

核心 API（可直接 import 使用）：

```python
from deflated_sharpe import deflated_sharpe_ratio, expected_max_sharpe

dsr = deflated_sharpe_ratio(est_sr=2.5/15.87, var_sr=0.5/252, n_trials=100,
                            t_len=1250, skew=-3.0, kurt=10.0)
```

## 菜三（硬菜）：Almgren-Chriss 最优执行模拟器 📈

`03_执行系统/almgren_chriss.py`（运行输出见 `output.txt`）

对应第 19 课（执行系统）+ arXiv:1906.11046（AC 模型当 RL 环境）+ Almgren & Chriss (2000)。**"卖得快→冲击大，卖得慢→风险大"**的完整量化：

```bash
cd quant-study/03_执行系统
python3 almgren_chriss.py
```

1. **AC 最优轨迹**：κT 扫描显示风险厌恶如何决定清仓节奏（κT=4 前三期卖 70%，TWAP 只卖 30%）
2. **成本-风险权衡**：MC 5000 条路径——立即卖出成本 15,534/风险 1,996 vs TWAP 成本 4,242/风险 3,932，AC 在中间选切点
3. **高频敏感警告**：同一份日 alpha 2,000 元，日频净赚 1,850，拆成 48 笔后倒亏 5,200（第 19 课"手续费收割机"警告的数学版）

## 菜四（甜品）：多智能体交易系统骨架 🤖

`04_多智能体系统/multi_agent_trading.py`（运行输出见 `output.txt`）

对应第 11 课（分工/投票/一票否决）+ 第 12 课（Regime）+ 第 15 课（风控）+ 第 19 课（执行）+ TradingGPT/FinMem 记忆思想：

```bash
cd quant-study/04_多智能体系统
python3 multi_agent_trading.py            # 完整多智能体
python3 multi_agent_trading.py --no-risk  # 对照组
```

500 天合成行情（趋势牛→震荡→危机→震荡→趋势熊）跑完整决策循环，结果：

| 指标 | 多智能体（含一票否决） | 对照组（无风控） |
|---|---|---|
| 总收益 | **+12.6%** | +5.7% |
| 年化夏普 | **0.70** | 0.30 |
| 最大回撤 | **15.1%** | 22.8% |

亮点：第 243 天危机已至但 Regime（20 日均线滞后）仍判"趋势牛"，**Risk Agent 单日亏损熔断抢先兜底**——分工与一票否决的价值瞬间；Risk Agent 带 FinMem 式去重记忆，吃过亏后下次危机直接空仓。

## 菜五（硬菜2）：手写高斯 HMM 市场状态识别 🧠

`05_regime检测/hmm_regime.py`（运行输出见 `output.txt`）

对应第 12 课（市场状态识别）。环境里没有 hmmlearn，**从零手搓 Baum-Welch + Viterbi**（纯 numpy，无第三方依赖），顺带把 EM 算法的每个细节看懂：

```bash
cd quant-study/05_regime检测
python3 hmm_regime.py
```

1. **参数学习**：三状态合成市场（趋势/震荡/危机），HMM 学到的均值/波动率与真实设定高度吻合，转移矩阵强对角（状态有粘性），识别准确率 **81.3%**
2. **vs 规则法**：HMM 准确率 81.3% vs 波动率聚类规则法 60.4%；平均切换滞后 5.8 天 vs 57.8 天
3. **Regime 值多少钱**：复刻第 12 课核心公式——净增值 = 收益提升 − 切换成本。固定 50/50 两专家互相抵消 -47%；HMM 动态分配 +171%（切换成本 0.1%）递减到 +107%（3%），单调验证"切得越勤死得越快"

调试彩蛋：实现中踩了混合高斯 EM 的经典坑——观测似然按"状态内缩放"会抹掉状态间相对大小导致塌缩，按"时刻缩放"即修复（代码注释里保留了教训）。

## 菜六（汤）：量化系统"12 种死亡方式"自动体检器 ⚰️

`06_死亡方式体检/strategy_autopsy.py`（运行输出见 `output.txt`）

对应附录 B（12 种典型死亡方式 + 综合诊断表 + 每周健康检查清单），把前五道菜串成一个**策略尸检工具**——输入收益序列，自动输出 12 项体检报告：

```bash
cd quant-study/06_死亡方式体检
python3 strategy_autopsy.py                          # 演示：问题策略 vs 健康策略
python3 strategy_autopsy.py --file returns.csv --n-trials 200 --slippage-bp 5 \
        --turnover 0.5 --leverage 1.0                # 体检你自己的策略
```

- **可自动检测（8 项）**：#1 数据污染（|z|>6 跳变/零收益）、#2 过拟合（复用菜二 DSR！）、#3 Regime 漂移（前后半段 z 检验）、#4 执行失真（滑点成本/毛收益）、#5 风控失效（单日亏损/回撤熔断线）、#7 相关性飙升（危机窗口 vs 全期）、#8 杠杆爆仓（回撤反推安全杠杆）、#12 对手盘适应（滚动收益衰减 z 检验）
- **N/A（4 项）**：#6 流动性、#9 人为干预、#10 系统故障、#11 监管变化——需外部数据/日志，输出处方
- 演示结果：**问题策略健康度 0%**（7 病危+1 风险全抓到）vs **健康策略 100%**

## 菜七（汤）：HFT 高频交易与工具资料库 🚀

`07_高频交易与工具资料库.md`

搜罗 HFT 三线资料：四基石论文 + 5 本必读书 + 3 门免费课程 + 研究者名单；分析/数学工具矩阵（hftbacktest / Hummingbot / Qlib / statsmodels / QuantLib…）；政治经济逻辑经典（Dalio 债务周期、Minsky FIH、Krishnamurthy-Lustig 美元清算价、MSCI 地缘冲击 Playbook、Graham Capital 宏观 regime…）。核心经验：地缘事件只有传导到"供给→通胀→央行"才改变趋势，否则一个月内修复；2022 后股债对冲失效，黄金+美元成新对冲主角。

## 菜八（大菜）：hftbacktest 系统学习 + 做市工具链 🏎️

`08_hftbacktest系统学习.md`

从零精读 hftbacktest 官方全部核心教程（Getting Started / Data Preparation / Probability Queue Models / GLFT / OBI Alpha / Impact of Order Latency / Level-3 / Pricing Framework），沉淀为 MCP 工具：

- **GLFT 做市校准**（`calibrate_glft_mm`）：交易强度 λ(δ)=A·e^(-kδ) 校准 → 波动率 → 最优半价差/skew（式 4.6/4.7）→ 打穿概率诊断
- **微观结构信号**（`compute_microstructure_signals`）：OBI / VAMP / Effective-VAMP / Weighted-Depth + 标准化 + IC 信息系数
- **延迟影响模拟**（`simulate_latency_impact`）：复刻"延迟决定生死"——0/3/10 步延迟 PnL 依次递减

关键数值（官方实测）：ETHUSDT 同策略 5 天，feed 延迟回测 SR −0.20、真实延迟 +1.54、放大延迟 −0.38；GLFT 裸模型 SR −246 → adj2=0.05 → +1.2 → 20 档网格 +19.8；OBI 做市 SR 10.8(2023)→5.4(2025-02)→3.0(2025-05)，两年衰减 3.6 倍；BTC alpha 迁移到 ETH/XRP/SOL/DOGE 均有效（SR 19.8~28.7）。

---

### 一句话总结八道菜

- **菜一**：书单要按勘误后的链接读，否则 40% 的时间会花在医学影像和弦论上。
- **菜二**：任何回测夏普都必须回答 5 个问题（试验次数 N、Var[SR]、T、偏度、峰度），回答不了就是薛定谔的夏普。
- **菜三**：显示价 ≠ 成交价；没有免费午餐，只有用 AC 框架可选择的代价。
- **菜四**：别当"全能 Agent"——外科医生不该同时当麻醉师；一票否决是资金最后防线。
- **菜五**：Regime 检测的价值 = 识别准确性 × 策略差异 − 切换成本；过渡期宁可模糊，不可硬切。
- **菜六**：初级课程教你怎么赚钱，高级课程教你怎么死——先体检，再上路。
- **菜七**：HFT 的 alpha 在衰减（OBI 做市 SR 10.8→3.0），散户的对手是返佣结构和延迟；政治经济逻辑里最值钱的一条——地缘事件只有传导到"供给→通胀→央行"才改变趋势，否则一个月内修复。
- **菜八**：回测里没有延迟 = 凭空多出几毫秒 alpha；做市利润 = 价差 + 返佣 − 库存 − 逆向选择，返佣是主粮；skew 过强 = 不敢持仓 = 亏损；信号按年复查衰减，组合权重是过拟合高发区。
