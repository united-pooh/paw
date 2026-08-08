"""统一数据源：akshare / baostock / tushare，自动回退。

设计原则（吸收自 Vibe-Trading data-routing skill）：
- A 股日线优先 tushare（有 token 时，数据质量最好）→ akshare → baostock
- 无 token 时 akshare 优先，失败自动回退 baostock
- 数据源在函数内延迟 import：未安装的源自动跳过，不阻塞服务器启动
- 所有外部调用 try/catch：单源失败返回可操作错误，绝不崩溃
"""

from __future__ import annotations

import io
import os
from contextlib import redirect_stderr, redirect_stdout
from datetime import date, timedelta
from typing import Any

ANNUAL = 252

# ---------------------------------------------------------------------------
# 行情直连：公开行情接口绕过本地代理
# 多数网络环境（公司代理/VPN）会拦截行情域名或返回验证页，而行情域名
# 本身是公开可直连的。requests/urllib3 每次请求都会重新读取 no_proxy，
# 这里在模块加载时把行情域名写入 no_proxy，运行时即时生效。
# ---------------------------------------------------------------------------

_NO_PROXY_DOMAINS = (
    ".eastmoney.com",     # 东方财富：push2/push2his 行情快照与 K 线
    ".sina.com.cn",       # 新浪财经：全市场快照
    ".sinajs.cn",         # 新浪行情（hq.sinajs.cn）
    ".gtimg.cn",          # 腾讯行情
    ".10jqka.com.cn",     # 同花顺
    ".126.net",           # 网易财经
    ".tushare.pro",       # tushare API
    ".baostock.com",      # baostock 登录
)


def _bypass_proxy_for_quotes() -> None:
    cur = (os.environ.get("NO_PROXY") or os.environ.get("no_proxy") or "").strip(",")
    missing = [d for d in _NO_PROXY_DOMAINS if d not in cur]
    if missing:
        merged = ",".join([cur] + missing).strip(",")
        os.environ["NO_PROXY"] = merged
        os.environ["no_proxy"] = merged


_bypass_proxy_for_quotes()

# ---------------------------------------------------------------------------
# 内部工具
# ---------------------------------------------------------------------------


def _default_range(days: int = 250) -> tuple[str, str]:
    """默认时间范围：今天往前 days 个自然日（YYYYMMDD 字符串）。"""
    end = date.today()
    start = end - timedelta(days=int(days * 1.6))
    return start.strftime("%Y%m%d"), end.strftime("%Y%m%d")


def _ak_symbol(symbol: str) -> str:
    """akshare 使用纯 6 位代码（去掉 .SZ/.SH 后缀）。"""
    return symbol.split(".")[0]


def _bs_symbol(symbol: str) -> str:
    """baostock 使用 sz.000001 / sh.600000 格式。"""
    s = _ak_symbol(symbol)
    if s.startswith(("5", "6", "9")):
        return f"sh.{s}"
    if s.startswith(("4", "8")):
        return f"bj.{s}"
    return f"sz.{s}"


def _ts_symbol(symbol: str) -> str:
    """tushare 使用 000001.SZ 格式。"""
    s = _ak_symbol(symbol)
    if s.startswith(("6", "9")):
        return f"{s}.SH"
    if s.startswith(("4", "8")):
        return f"{s}.BJ"
    return f"{s}.SZ"


# ---------------------------------------------------------------------------
# 单源实现
# ---------------------------------------------------------------------------


def _em_market(symbol: str) -> int:
    """东方财富 secid 的市场编号：沪市=1，深/北市=0。"""
    code = _ak_symbol(symbol)
    return 1 if code.startswith(("5", "6", "9")) else 0


def _em_fetch_kline(
    symbol: str, period: str, start: str, end: str, adjust: str
) -> list[dict[str, Any]]:
    """直接调用东方财富 push2his K 线接口，不经过 AKShare。"""
    import requests

    klt = {"daily": "101", "1": "1", "5": "5", "15": "15", "30": "30", "60": "60"}[period]
    fqt = {"none": "0", "qfq": "1", "hfq": "2"}[adjust]
    session = requests.Session()
    session.trust_env = True
    headers = {
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/131.0 Safari/537.36",
        "Referer": "https://quote.eastmoney.com/",
        "Accept": "application/json,text/plain,*/*",
        "Connection": "keep-alive",
    }
    params = {
        "secid": f"{_em_market(symbol)}.{_ak_symbol(symbol)}",
        "ut": "fa5fd1943c7b386f172d6893dbfba10b",
        "fields1": "f1,f2,f3,f4,f5,f6",
        "fields2": "f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61",
        "klt": klt,
        "fqt": fqt,
        "beg": start,
        "end": end,
    }
    response = None
    errors = []
    # 东财两个公开行情域名通常提供同一接口；网络代理/出口可能只允许其中一个。
    for host in ("push2his.eastmoney.com", "push2.eastmoney.com"):
        try:
            response = session.get(
                f"https://{host}/api/qt/stock/kline/get",
                params=params,
                headers=headers,
                timeout=15,
            )
            response.raise_for_status()
            break
        except Exception as exc:  # noqa: BLE001 — 尝试备用东财域名
            errors.append(f"{host}: {exc}")
            response = None
    if response is None:
        raise RuntimeError("东方财富 K 线接口不可用: " + "; ".join(errors))
    payload = response.json()
    data = payload.get("data") or {}
    klines = data.get("klines") or []
    out = []
    for item in klines:
        fields = str(item).split(",")
        if len(fields) < 7:
            continue
        try:
            out.append({
                "date": fields[0][:19],
                "open": float(fields[1]),
                "close": float(fields[2]),
                "high": float(fields[3]),
                "low": float(fields[4]),
                "volume": float(fields[5] or 0),
                "amount": float(fields[6] or 0),
                "pct_chg": float(fields[8] or 0) if len(fields) > 8 else 0.0,
            })
        except (TypeError, ValueError):
            continue
    return out


def _ak_fetch_kline(
    symbol: str, period: str, start: str, end: str, adjust: str
) -> list[dict[str, Any]]:
    import akshare as ak

    if period in ("1", "5", "15", "30", "60"):
        df = ak.stock_zh_a_hist_min_em(
            symbol=_ak_symbol(symbol),
            period=period,
            start_date=start,
            end_date=end,
            adjust="" if adjust == "none" else "qfq",
        )
        if df is None or df.empty:
            return []
        out = []
        for _, r in df.iterrows():
            out.append(
                {
                    "date": str(r["时间"])[:19],
                    "open": float(r["开盘"]),
                    "high": float(r["最高"]),
                    "low": float(r["最低"]),
                    "close": float(r["收盘"]),
                    "volume": float(r.get("成交量", 0) or 0),
                    "amount": float(r.get("成交额", 0) or 0),
                }
            )
        return out
    df = ak.stock_zh_a_hist(
        symbol=_ak_symbol(symbol),
        period="daily",
        start_date=start,
        end_date=end,
        adjust=adjust if adjust != "none" else "",
    )
    if df is None or df.empty:
        return []
    out = []
    for _, r in df.iterrows():
        out.append(
            {
                "date": str(r["日期"])[:10],
                "open": float(r["开盘"]),
                "high": float(r["最高"]),
                "low": float(r["最低"]),
                "close": float(r["收盘"]),
                "volume": float(r["成交量"]),
                "amount": float(r["成交额"]),
                "pct_chg": float(r.get("涨跌幅", 0) or 0),
            }
        )
    return out


def _bs_fetch_kline(
    symbol: str, period: str, start: str, end: str, adjust: str
) -> list[dict[str, Any]]:
    import baostock as bs

    if period not in ("1", "5", "15", "30", "60", "daily"):
        raise ValueError(f"baostock 不支持周期 {period}（支持 1/5/15/30/60/daily）")
    freq = "d" if period == "daily" else period
    # adjustflag: 1=后复权 2=前复权 3=不复权
    adjflag = {"none": "3", "qfq": "2", "hfq": "1"}.get(adjust, "3")
    # baostock 日期格式是 YYYY-MM-DD（不是 YYYYMMDD）
    start_fmt = f"{start[:4]}-{start[4:6]}-{start[6:]}"
    end_fmt = f"{end[:4]}-{end[4:6]}-{end[6:]}"
    with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
        lg = bs.login()
    if lg is None:
        raise RuntimeError("baostock 登录失败（login 返回 None，可能网络受限）")
    if getattr(lg, "error_code", "0") != "0":
        raise RuntimeError(f"baostock 登录失败: {getattr(lg, 'error_msg', 'unknown')}")
    try:
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            rs = bs.query_history_k_data_plus(
                _bs_symbol(symbol),
                "date,open,high,low,close,volume,amount,pctChg",
                start_date=start_fmt,
                end_date=end_fmt,
                frequency=freq,
                adjustflag=adjflag,
            )
        if rs is None:
            raise RuntimeError("baostock 查询返回 None（可能网络受限）")
        if getattr(rs, "error_code", "0") != "0":
            raise RuntimeError(f"baostock 查询失败: {getattr(rs, 'error_msg', 'unknown')}")
        out = []
        while rs.next():
            row = rs.get_row_data()
            try:
                out.append(
                    {
                        "date": row[0],
                        "open": float(row[1]),
                        "high": float(row[2]),
                        "low": float(row[3]),
                        "close": float(row[4]),
                        "volume": float(row[5]),
                        "amount": float(row[6]),
                        "pct_chg": float(row[7]) if row[7] else 0.0,
                    }
                )
            except (TypeError, ValueError):
                continue
        return out
    finally:
        try:
            with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                bs.logout()
        except Exception:  # noqa: BLE001
            pass


def _ts_fetch_kline(
    symbol: str, period: str, start: str, end: str, adjust: str
) -> list[dict[str, Any]]:
    import tushare as ts

    token = os.getenv("TUSHARE_TOKEN")
    if not token:
        raise RuntimeError("TUSHARE_TOKEN 未设置，无法使用 tushare")
    pro = ts.pro_api(token)
    if period == "daily":
        df = ts.pro_bar(
            ts_code=_ts_symbol(symbol),
            api=pro,
            adj=adjust if adjust != "none" else None,
            start_date=start,
            end_date=end,
        )
        if df is None or df.empty:
            return []
        df = df.sort_values("trade_date")
        out = []
        for _, r in df.iterrows():
            out.append(
                {
                    "date": str(r["trade_date"]),
                    "open": float(r["open"]),
                    "high": float(r["high"]),
                    "low": float(r["low"]),
                    "close": float(r["close"]),
                    "volume": float(r.get("vol", 0) or 0),
                    "amount": float(r.get("amount", 0) or 0),
                    "pct_chg": float(r.get("pct_chg", 0) or 0),
                }
            )
        return out
    # 分钟线需要高积分，返回明确错误提示
    raise RuntimeError(
        "tushare 分钟行情需积分 ≥2000，请用 source='akshare' 拉分钟线"
    )


_LAST_REALTIME_ERROR: str = ""


def _ak_fetch_realtime(symbols: list[str]) -> list[dict[str, Any]]:
    import akshare as ak

    global _LAST_REALTIME_ERROR
    try:
        df = ak.stock_zh_a_spot_em()
        if df is None or df.empty:
            return []
    except Exception as e:  # noqa: BLE001 — 东财失败返回空，交给上层回退新浪
        _LAST_REALTIME_ERROR = f"东财: {e}"
        return []
    if symbols:
        wanted = {_ak_symbol(s) for s in symbols}
        df = df[df["代码"].isin(wanted)]
    out = []
    for _, r in df.iterrows():
        try:
            out.append(
                {
                    "symbol": str(r["代码"]),
                    "name": str(r["名称"]),
                    "price": float(r.get("最新价", 0) or 0),
                    "pct_chg": float(r.get("涨跌幅", 0) or 0),
                    "change": float(r.get("涨跌额", 0) or 0),
                    "volume": float(r.get("成交量", 0) or 0),
                    "amount": float(r.get("成交额", 0) or 0),
                    "amplitude": float(r.get("振幅", 0) or 0),
                    "turnover": float(r.get("换手率", 0) or 0),
                    "volume_ratio": float(r.get("量比", 0) or 0),
                    "pe": float(r.get("市盈率-动态", 0) or 0),
                    "pb": float(r.get("市净率", 0) or 0),
                }
            )
        except (TypeError, ValueError):
            continue
    return out


# ---------------------------------------------------------------------------
# 统一入口（自动回退）
# ---------------------------------------------------------------------------

_SOURCES = ("eastmoney", "akshare", "baostock", "tushare")


def fetch_kline_with_source(
    symbol: str,
    period: str = "daily",
    start_date: str | None = None,
    end_date: str | None = None,
    adjust: str = "qfq",
    source: str = "auto",
) -> tuple[list[dict[str, Any]], str]:
    """获取 A 股 K 线（日线/分钟线），多数据源自动回退，返回 (rows, 实际使用的源名)。

    Args:
        symbol: 6 位代码或带后缀（000001 / 000001.SZ）
        period: daily 或 1/5/15/30/60（分钟）
        start_date / end_date: YYYYMMDD；默认最近约 250 个交易日
        adjust: none / qfq（前复权）/ hfq（后复权）
    source: auto / eastmoney / akshare / baostock / tushare
    """
    if period not in ("daily", "1", "5", "15", "30", "60"):
        raise ValueError(f"period 必须是 daily/1/5/15/30/60，收到 {period}")
    if adjust not in ("none", "qfq", "hfq"):
        raise ValueError(f"adjust 必须是 none/qfq/hfq，收到 {adjust}")
    if not start_date or not end_date:
        s, e = _default_range()
        start_date, end_date = start_date or s, end_date or e

    order = {
        "auto": ["eastmoney", "tushare", "akshare", "baostock"],
        "eastmoney": ["eastmoney"],
        "akshare": ["akshare"],
        "baostock": ["baostock"],
        "tushare": ["tushare"],
    }[source]

    errors: list[str] = []
    for name in order:
        try:
            if name == "eastmoney":
                rows = _em_fetch_kline(symbol, period, start_date, end_date, adjust)
            elif name == "akshare":
                rows = _ak_fetch_kline(symbol, period, start_date, end_date, adjust)
            elif name == "baostock":
                rows = _bs_fetch_kline(symbol, period, start_date, end_date, adjust)
            else:
                rows = _ts_fetch_kline(symbol, period, start_date, end_date, adjust)
            if rows:
                return rows, name
            errors.append(f"{name}: 返回空数据")
        except Exception as e:  # noqa: BLE001 — 单源失败要回退而非崩溃
            errors.append(f"{name}: {e}")
    raise RuntimeError(
        f"所有数据源均失败（{symbol} {period}）: {'; '.join(errors)}"
    )


def fetch_kline(
    symbol: str,
    period: str = "daily",
    start_date: str | None = None,
    end_date: str | None = None,
    adjust: str = "qfq",
    source: str = "auto",
) -> list[dict[str, Any]]:
    """获取 A 股 K 线（日线/分钟线），多数据源自动回退（只返回行，源名见 with_source 版）。"""
    rows, _ = fetch_kline_with_source(
        symbol, period=period, start_date=start_date,
        end_date=end_date, adjust=adjust, source=source,
    )
    return rows


def fetch_realtime_quotes(
    symbols: list[str] | None = None, limit: int = 100
) -> list[dict[str, Any]]:
    """获取实时行情快照（akshare 东财全市场，按代码过滤）。

    Args:
        symbols: 要查询的代码列表；None = 全市场
        limit: 返回条数上限（防刷屏）
    """
    global _LAST_REALTIME_ERROR
    rows = _ak_fetch_realtime(symbols or [])
    if not rows:
        # 东财源失败时尝试新浪源
        try:
            import akshare as ak

            df = ak.stock_zh_a_spot()  # 新浪源
            if df is not None and not df.empty:
                if symbols:
                    wanted = {_ak_symbol(s) for s in symbols}
                    # 新浪"代码"列带 sh/sz/bj 前缀（如 sh600519），归一化为 6 位匹配
                    df = df[df["代码"].astype(str).str[-6:].isin(wanted)]
                rows = [
                    {
                        "symbol": str(r["代码"]),
                        "name": str(r["名称"]),
                        "price": float(r.get("最新价", 0) or 0),
                        "pct_chg": float(r.get("涨跌幅", 0) or 0),
                        "change": float(r.get("涨跌额", 0) or 0),
                        "volume": float(r.get("成交量", 0) or 0),
                        "amount": float(r.get("成交额", 0) or 0),
                        "turnover": float(r.get("换手率", 0) or 0),
                    }
                    for _, r in df.iterrows()
                ]
        except Exception as e:  # noqa: BLE001
            _LAST_REALTIME_ERROR = f"{_LAST_REALTIME_ERROR}; 新浪: {e}"
    if not rows:
        raise RuntimeError(
            f"实时行情获取失败（东财/新浪源均不可用）: {_LAST_REALTIME_ERROR}"
        )
    return rows[:limit]


def fetch_stock_list() -> list[dict[str, Any]]:
    """获取 A 股股票列表（代码/名称/行业/上市日期）。"""
    import akshare as ak

    try:
        df = ak.stock_info_a_code_name()
        rows = [
            {"symbol": str(r["code"]), "name": str(r["name"])}
            for _, r in df.iterrows()
        ]
        if rows:
            return rows
    except Exception as e:  # noqa: BLE001
        last_err = str(e)
    # 回退：tushare
    token = os.getenv("TUSHARE_TOKEN")
    if token:
        try:
            import tushare as ts

            pro = ts.pro_api(token)
            df = pro.stock_basic(
                list_status="L", fields="ts_code,symbol,name,industry,list_date"
            )
            return [
                {
                    "symbol": str(r["symbol"]),
                    "name": str(r["name"]),
                    "industry": str(r.get("industry") or ""),
                    "list_date": str(r.get("list_date") or ""),
                }
                for _, r in df.iterrows()
            ]
        except Exception as e:  # noqa: BLE001
            last_err = str(e)
    raise RuntimeError(f"股票列表获取失败: {last_err}")
