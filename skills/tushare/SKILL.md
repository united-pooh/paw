---
name: tushare
category: data-source
description: Tushare 财经数据接口（需 token）。股票/基金/期货/宏观/财务等全品类数据，标准化 API。A 股 OHLCV 首选数据源之一。
source: HKUDS/Vibe-Trading（MIT，吸收自 skillhub）
---

# Tushare 数据源

## 快速上手

```bash
pip install tushare -i https://pypi.tuna.tsinghua.edu.cn/simple
export TUSHARE_TOKEN=your_token   # https://tushare.pro/register 注册获取
```

```python
import os
import tushare as ts
token = os.getenv('TUSHARE_TOKEN') or ts.get_token()
pro = ts.pro_api(token)

# 股票列表
df = pro.stock_basic(list_status='L', fields='ts_code,symbol,name,area,industry,list_date')
# 日线行情（前复权）
df = pro.daily(ts_code='000001.SZ', start_date='20240101', end_date='20260101')
# 复权因子
adj = pro.adj_factor(ts_code='000001.SZ')
```

## 参数格式

- 日期：`YYYYMMDD`（如 20241231）
- 股票代码：`ts_code` 格式（000001.SZ / 600000.SH / 430139.BJ）
- 返回：pandas DataFrame

## 常用接口速查（按数据需求路由）

| 数据需求 | 接口 | 备注 |
|---------|------|------|
| 股票列表 | `stock_basic` | 免费 |
| 日线行情 | `daily` | 免费，未复权；复权用 `adj_factor` 自行计算 |
| 复权行情 | `pro_bar(ts_code=..., adj='qfq/hfq')` | 免费 |
| 分钟行情 | `stk_mins` | 需积分 ≥2000（专业版），否则返回空 |
| 实时行情 | `rt_k` / `rt_min` | 需积分 |
| 财务指标 | `fina_indicator` / `income` / `balancesheet` / `cashflow` | 需积分 |
| 涨跌停价 | `stk_limit` | 模拟盘校验用 |
| 交易日历 | `trade_cal` | 免费 |
| 资金流向 | `moneyflow` | 需积分 |
| 龙虎榜 | `top_list` / `top_inst` | 需积分 |
| 宏观经济 | `cn_gdp` / `cn_cpi` / `shibor` 等 | 部分免费 |

## 路由与回退

- **有 token 时**：A 股日线优先 tushare（数据质量好）
- **无 token 时**：自动回退 akshare / baostock（免费免注册）
- 分钟级数据 tushare 积分门槛高，优先 akshare `stock_zh_a_hist_min_em()`

## 红线

- token 走环境变量，不硬编码
- 积分不足的接口会返回空 DataFrame，调用方需识别并回退，不要静默失败
