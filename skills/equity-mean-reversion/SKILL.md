---
name: equity-mean-reversion
description: A股/ETF 震荡与高波动行情的均值回归策略研究技能。只要用户提到震荡市、急跌反弹、均值回归、布林带、接飞刀风险、想用 quant-mcp 的 mean_reversion_backtest，或希望比较防守趋势策略与震荡策略，就使用本技能。负责准备真实历史数据、调用回测工具、比较买入持有、披露交易次数与数据源，并明确区分“低回撤来自少交易”和真正的风险控制。仅用于研究模拟，不给出真实下单指令。
---

# Equity Mean Reversion

## 目标
将震荡/高波动适配策略作为防守趋势策略的补充研究模块，而不是声称它能普遍提高收益。核心假设是：价格短期显著偏离滚动均值后，若出现反弹确认且波动率没有失控，可能回归均值附近。

对应 quant-mcp 工具：`mean_reversion_backtest`。

## 默认参数

- `lookback=20`
- `entry_z=-1.5`，`exit_z=-0.1`
- `max_hold_days=10`
- `vol_window=20`，`max_annual_vol=0.65`
- `target_vol=0.15`，`max_weight=0.5`
- `fee_bp=5`
- 收盘确认信号，次日开盘成交；long-only，不做空

## 工作流

### 1. 明确研究范围
确认或合理推断标的、回测区间、复权方式、数据源、成本及比较基线。相对日期必须展开成具体日期并写明实际交易日范围。

### 2. 获取数据
优先调用：

```text
fetch_kline(symbol=代码, period="daily", start_date=YYYYMMDD,
            end_date=YYYYMMDD, adjust="qfq", source="eastmoney")
```

东方财富失败时可使用 `source="baostock"`，但必须标注实际 `source_used`，禁止把 BaoStock 结果写成东方财富结果。批量研究中记录失败代码和原因，不伪造缺失数据。

### 3. 运行策略

```text
mean_reversion_backtest(
  rows=K线行列表, lookback=20, entry_z=-1.5, exit_z=-0.1,
  max_hold_days=10, vol_window=20, max_annual_vol=0.65,
  target_vol=0.15, max_weight=0.5, fee_bp=5.0,
  evaluation_start_date=YYYY-MM-DD
)
```

策略逻辑：价格相对滚动均值 z-score 足够低、且高于短期均线或出现最近3日反弹确认，同时波动率不过高；次日开盘按目标波动率缩放仓位。回到均值、超过持有期或波动率超限时退出。

### 4. 必须比较的指标
至少报告策略收益、买入持有收益、年化夏普、最大回撤、交易次数、总成本、有效观测数和是否跑赢买入持有。多资产还报告有效标的数、正收益数、跑赢数、平均/中位数收益与回撤、平均交易次数及失败原因。

### 5. 防止误读
如果多数标的交易次数为0或极低，不得把低回撤称为优秀风控；明确说明策略主要持有现金，并报告参与率。该策略在单边下跌中可能接刀，在持续上涨中可能错过收益，属于条件性补充而非通用策略。

### 6. 参数研究纪律
先记录固定基线，再在训练区间搜索、测试区间验证；披露参数组合数，不选仅凭短样本收益最高的组合。扫描 lookback、entry_z、exit_z、max_hold_days、max_annual_vol 时同时观察收益、回撤、夏普、交易次数和成本；必要时调用 `calc_deflated_sharpe`。

## 报告模板

```markdown
# 均值回归策略研究报告
## 研究设定
- 区间：
- 标的与有效样本：
- 数据源及实际 source_used：
- 复权方式：
- 成本：
- 参数：
## 策略规则
## 汇总结果
| 指标 | 均值回归 | 买入持有 | 说明 |
|---|---:|---:|---|
| 收益 | | | |
| 年化夏普 | | | |
| 最大回撤 | | | |
| 交易次数 | | | |
| 成本 | | | |
## 多资产泛化
## 适配性判断
分别讨论震荡、下跌、高波动、单边上涨和V型反转。
## 结论与限制
```

## 工具配合

- `defensive_equity_filter`：趋势/动量防守基线；
- `backtest_etf`：MA趋势策略基线；
- `detect_regime_hmm`：识别震荡、趋势、危机并路由；
- `calc_deflated_sharpe`：评估多参数试验后的夏普可信度；
- `autopsy_strategy`：足够长收益序列的策略体检。

## 安全边界
仅用于研究和模拟，不连接真实下单；不把回测当作收益承诺，不编造数据，不用单一短样本宣称泛化。真实使用前进行样本外、成本压力和模拟盘验证。
