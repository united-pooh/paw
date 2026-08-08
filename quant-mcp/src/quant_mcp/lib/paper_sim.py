"""SQLite 模拟盘引擎：账户 / 委托 / 持仓 / 成交 / 资金流水 / 日终结算。

A 股规则：T+1（当日买入不可卖）、100 股整数倍（可关）、
佣金万 2.5 最低 5 元、卖出印花税 0.05%。

设计要点：
- 每次操作独立短连接（check_same_thread=False + WAL），MCP 并发安全
- 所有校验在成交前完成，失败返回废单原因，绝不穿仓
- T+1 用 day_bought_qty 追踪：交易日变化时自动解冻
"""

from __future__ import annotations

import os
import sqlite3
import uuid
from contextlib import closing
from datetime import date
from typing import Any

COMMISSION_RATE = 0.00025   # 万 2.5
COMMISSION_MIN = 5.0        # 最低 5 元
STAMP_TAX = 0.0005          # 卖出印花税 0.05%
LOT_SIZE = 100              # A 股整手


def default_db_path() -> str:
    """默认数据库位置：$QUANT_MCP_DB_DIR 或 ~/.quant-mcp/paper_trading.db。"""
    d = os.getenv("QUANT_MCP_DB_DIR") or os.path.join(
        os.path.expanduser("~"), ".quant-mcp"
    )
    os.makedirs(d, exist_ok=True)
    return os.path.join(d, "paper_trading.db")


def _connect(db_path: str) -> sqlite3.Connection:
    conn = sqlite3.connect(db_path, check_same_thread=False)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA busy_timeout=5000")
    return conn


def _init_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(
        """
        CREATE TABLE IF NOT EXISTS accounts (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            initial_cash REAL NOT NULL,
            cash REAL NOT NULL,
            created_at TEXT NOT NULL,
            closed_at TEXT
        );
        CREATE TABLE IF NOT EXISTS orders (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            account_id TEXT NOT NULL,
            symbol TEXT NOT NULL,
            direction TEXT NOT NULL CHECK (direction IN ('buy','sell')),
            order_type TEXT NOT NULL CHECK (order_type IN ('limit','market')),
            price REAL,
            qty INTEGER NOT NULL,
            status TEXT NOT NULL CHECK (status IN ('pending','filled','cancelled','rejected')),
            filled_qty INTEGER DEFAULT 0,
            filled_price REAL,
            fee REAL DEFAULT 0,
            reason TEXT,
            trade_date TEXT NOT NULL,
            created_at TEXT NOT NULL,
            filled_at TEXT
        );
        CREATE TABLE IF NOT EXISTS positions (
            account_id TEXT NOT NULL,
            symbol TEXT NOT NULL,
            qty INTEGER NOT NULL DEFAULT 0,
            day_bought_qty INTEGER NOT NULL DEFAULT 0,
            last_buy_date TEXT,
            avg_cost REAL NOT NULL DEFAULT 0,
            last_price REAL,
            updated_at TEXT NOT NULL,
            PRIMARY KEY (account_id, symbol)
        );
        CREATE TABLE IF NOT EXISTS trades (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            account_id TEXT NOT NULL,
            order_id INTEGER NOT NULL,
            symbol TEXT NOT NULL,
            direction TEXT NOT NULL,
            price REAL NOT NULL,
            qty INTEGER NOT NULL,
            fee REAL NOT NULL,
            amount REAL NOT NULL,
            trade_date TEXT NOT NULL,
            traded_at TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS cash_flows (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            account_id TEXT NOT NULL,
            trade_id INTEGER,
            delta REAL NOT NULL,
            reason TEXT NOT NULL,
            created_at TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS daily_snapshots (
            account_id TEXT NOT NULL,
            trade_date TEXT NOT NULL,
            cash REAL NOT NULL,
            market_value REAL NOT NULL,
            total_equity REAL NOT NULL,
            unrealized_pnl REAL NOT NULL,
            created_at TEXT NOT NULL,
            PRIMARY KEY (account_id, trade_date)
        );
        CREATE INDEX IF NOT EXISTS idx_orders_account ON orders(account_id);
        CREATE INDEX IF NOT EXISTS idx_trades_account ON trades(account_id);
        """
    )
    conn.commit()


def _now() -> str:
    from datetime import datetime

    return datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def _today() -> str:
    return date.today().isoformat()


def _ensure_account(conn: sqlite3.Connection, account_id: str) -> sqlite3.Row:
    row = conn.execute(
        "SELECT * FROM accounts WHERE id=? AND closed_at IS NULL", (account_id,)
    ).fetchone()
    if row is None:
        raise ValueError(f"账户 {account_id} 不存在或已关闭")
    return row


def create_account(
    name: str = "模拟盘",
    initial_cash: float = 1_000_000.0,
    db_path: str | None = None,
) -> dict:
    """创建模拟盘账户。"""
    if initial_cash <= 0:
        raise ValueError("初始资金必须为正数")
    account_id = uuid.uuid4().hex[:12]
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        conn.execute(
            "INSERT INTO accounts (id, name, initial_cash, cash, created_at) VALUES (?,?,?,?,?)",
            (account_id, name, initial_cash, initial_cash, _now()),
        )
        conn.commit()
    return {"account_id": account_id, "name": name, "initial_cash": initial_cash}


def _fee(direction: str, amount: float) -> float:
    commission = max(amount * COMMISSION_RATE, COMMISSION_MIN)
    tax = amount * STAMP_TAX if direction == "sell" else 0.0
    return round(commission + tax, 4)


def submit_order(
    account_id: str,
    symbol: str,
    direction: str,
    qty: int,
    price: float | None = None,
    order_type: str = "limit",
    trade_date: str | None = None,
    auto_fill: bool = True,
    lot_size: bool = True,
    db_path: str | None = None,
) -> dict:
    """提交模拟委托。

    auto_fill=True：立即成交（limit 按委托价，market 用给定 price 或实时行情价）。
    auto_fill=False：挂单（pending），之后用 fill_pending_orders 按价格撮合。

    Returns:
        dict: 委托记录 + 成交结果（含废单原因）
    """
    if direction not in ("buy", "sell"):
        raise ValueError("direction 必须是 buy/sell")
    if order_type not in ("limit", "market"):
        raise ValueError("order_type 必须是 limit/market")
    if qty <= 0:
        raise ValueError("数量必须为正数")
    if lot_size and qty % LOT_SIZE != 0:
        return _reject("A 股需 100 股整数倍", account_id, symbol, direction, qty,
                       price, order_type, trade_date, db_path)
    if order_type == "limit" and (price is None or price <= 0):
        raise ValueError("限价单必须提供正价格")
    trade_date = trade_date or _today()
    now = _now()

    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        acc = _ensure_account(conn, account_id)

        # market 单自动取价
        exec_price = price
        if order_type == "market" and auto_fill:
            if price is None:
                from .datasource import fetch_realtime_quotes

                quotes = fetch_realtime_quotes([symbol])
                if not quotes:
                    return _reject("无法获取实时价格，请手动传 price",
                                   account_id, symbol, direction, qty, price,
                                   order_type, trade_date, db_path)
                exec_price = quotes[0]["price"]
            if not exec_price or exec_price <= 0:
                return _reject("无效成交价", account_id, symbol, direction,
                               qty, price, order_type, trade_date, db_path)

        # 校验
        if direction == "buy":
            amount = exec_price * qty if exec_price else 0.0
            fee = _fee("buy", amount) if exec_price else 0.0
            if acc["cash"] < amount + fee:
                return _reject(
                    f"资金不足：需 {amount + fee:.2f}，可用 {acc['cash']:.2f}",
                    account_id, symbol, direction, qty, price, order_type,
                    trade_date, db_path)
        else:
            pos = conn.execute(
                "SELECT * FROM positions WHERE account_id=? AND symbol=?",
                (account_id, symbol),
            ).fetchone()
            held = pos["qty"] if pos else 0
            sellable = held - (pos["day_bought_qty"] if pos and pos["last_buy_date"] == trade_date else 0)
            if qty > sellable:
                return _reject(
                    f"可卖持仓不足：持有 {held}（当日买入 {pos['day_bought_qty'] if pos else 0} 不可卖），"
                    f"可卖 {sellable}",
                    account_id, symbol, direction, qty, price, order_type,
                    trade_date, db_path)

        if auto_fill and exec_price:
            return _fill(conn, account_id, symbol, direction, qty, exec_price,
                         order_type, trade_date, now)

        # 挂单
        cur = conn.execute(
            """INSERT INTO orders (account_id, symbol, direction, order_type,
               price, qty, status, trade_date, created_at)
               VALUES (?,?,?,?,?,?,?,?,?)""",
            (account_id, symbol, direction, order_type, price, qty,
             "pending", trade_date, now),
        )
        conn.commit()
        return _order_dict(conn, cur.lastrowid)


def _fill(conn: sqlite3.Connection, account_id: str, symbol: str,
          direction: str, qty: int, price: float, order_type: str,
          trade_date: str, now: str) -> dict:
    """执行成交（调用方已校验）。"""
    amount = round(price * qty, 4)
    fee = _fee(direction, amount)
    net = amount + fee if direction == "buy" else amount - fee

    if direction == "buy":
        conn.execute("UPDATE accounts SET cash = cash - ? WHERE id=?",
                     (net, account_id))
    else:
        conn.execute("UPDATE accounts SET cash = cash + ? WHERE id=?",
                     (net, account_id))

    pos = conn.execute(
        "SELECT * FROM positions WHERE account_id=? AND symbol=?",
        (account_id, symbol),
    ).fetchone()
    if direction == "buy":
        if pos is None:
            conn.execute(
                """INSERT INTO positions (account_id, symbol, qty, day_bought_qty,
                   last_buy_date, avg_cost, last_price, updated_at)
                   VALUES (?,?,?,?,?,?,?,?)""",
                (account_id, symbol, qty, qty, trade_date, price, price, now),
            )
        else:
            new_qty = pos["qty"] + qty
            new_cost = (pos["avg_cost"] * pos["qty"] + amount) / new_qty
            day_bought = pos["day_bought_qty"] + qty if pos["last_buy_date"] == trade_date else qty
            conn.execute(
                """UPDATE positions SET qty=?, day_bought_qty=?, last_buy_date=?,
                   avg_cost=?, last_price=?, updated_at=? WHERE account_id=? AND symbol=?""",
                (new_qty, day_bought, trade_date, round(new_cost, 4), price,
                 now, account_id, symbol),
            )
    else:  # sell
        new_qty = pos["qty"] - qty
        day_bought = pos["day_bought_qty"]
        if new_qty == 0:
            conn.execute("DELETE FROM positions WHERE account_id=? AND symbol=?",
                         (account_id, symbol))
        else:
            conn.execute(
                "UPDATE positions SET qty=?, last_price=?, updated_at=? WHERE account_id=? AND symbol=?",
                (new_qty, price, now, account_id, symbol),
            )

    cur = conn.execute(
        """INSERT INTO orders (account_id, symbol, direction, order_type,
           price, qty, status, filled_qty, filled_price, fee, trade_date,
           created_at, filled_at)
           VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)""",
        (account_id, symbol, direction, order_type, price, qty, "filled",
         qty, price, fee, trade_date, now, now),
    )
    order_id = cur.lastrowid
    cur = conn.execute(
        """INSERT INTO trades (account_id, order_id, symbol, direction, price,
           qty, fee, amount, trade_date, traded_at) VALUES (?,?,?,?,?,?,?,?,?,?)""",
        (account_id, order_id, symbol, direction, price, qty, fee, amount,
         trade_date, now),
    )
    trade_id = cur.lastrowid
    conn.execute(
        "INSERT INTO cash_flows (account_id, trade_id, delta, reason, created_at) VALUES (?,?,?,?,?)",
        (account_id, trade_id, -net if direction == "buy" else net,
         f"{direction} {symbol} {qty}@{price}", now),
    )
    conn.commit()
    return _order_dict(conn, order_id)


def _reject(reason: str, account_id: str, symbol: str, direction: str,
            qty: int, price: float | None, order_type: str,
            trade_date: str | None, db_path: str | None) -> dict:
    """写一条废单记录并返回。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        cur = conn.execute(
            """INSERT INTO orders (account_id, symbol, direction, order_type,
               price, qty, status, reason, trade_date, created_at)
               VALUES (?,?,?,?,?,?,?,?,?,?)""",
            (account_id, symbol, direction, order_type, price, qty,
             "rejected", reason, trade_date or _today(), _now()),
        )
        conn.commit()
        return _order_dict(conn, cur.lastrowid)


def _order_dict(conn: sqlite3.Connection, order_id: int) -> dict:
    row = conn.execute("SELECT * FROM orders WHERE id=?", (order_id,)).fetchone()
    return dict(row)


def cancel_order(order_id: int, db_path: str | None = None) -> dict:
    """撤单（仅 pending 状态可撤）。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        row = conn.execute("SELECT * FROM orders WHERE id=?", (order_id,)).fetchone()
        if row is None:
            raise ValueError(f"委托 {order_id} 不存在")
        if row["status"] != "pending":
            return dict(row) | {"note": f"委托状态为 {row['status']}，无法撤单"}
        conn.execute("UPDATE orders SET status='cancelled' WHERE id=?", (order_id,))
        conn.commit()
        return _order_dict(conn, order_id)


def fill_pending_orders(
    account_id: str,
    price_map: dict[str, float],
    trade_date: str | None = None,
    db_path: str | None = None,
) -> dict:
    """按最新价撮合挂单：买单价 ≤ 最新价 或 卖单价 ≥ 最新价 才成交。"""
    trade_date = trade_date or _today()
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        pendings = conn.execute(
            "SELECT * FROM orders WHERE account_id=? AND status='pending' ORDER BY id",
            (account_id,),
        ).fetchall()
        filled, skipped = [], []
        for o in pendings:
            px = price_map.get(o["symbol"])
            if px is None:
                skipped.append({"order_id": o["id"], "symbol": o["symbol"],
                                "reason": "未提供最新价"})
                continue
            hit = (o["direction"] == "buy" and px <= o["price"]) or (
                o["direction"] == "sell" and px >= o["price"]
            )
            if not hit:
                skipped.append({"order_id": o["id"], "symbol": o["symbol"],
                                "price": o["price"], "last": px,
                                "reason": "价格未触发"})
                continue
            # 重新校验资金/持仓（撮合时可能已变化）
            acc = _ensure_account(conn, account_id)
            amount = o["price"] * o["qty"]
            fee = _fee(o["direction"], amount)
            if o["direction"] == "buy" and acc["cash"] < amount + fee:
                conn.execute("UPDATE orders SET status='rejected', reason=? WHERE id=?",
                             (f"撮合时资金不足", o["id"]))
                skipped.append({"order_id": o["id"], "reason": "撮合时资金不足"})
                continue
            if o["direction"] == "sell":
                pos = conn.execute(
                    "SELECT * FROM positions WHERE account_id=? AND symbol=?",
                    (account_id, o["symbol"]),
                ).fetchone()
                held = pos["qty"] if pos else 0
                sellable = held - (pos["day_bought_qty"] if pos and pos["last_buy_date"] == trade_date else 0)
                if o["qty"] > sellable:
                    conn.execute("UPDATE orders SET status='rejected', reason=? WHERE id=?",
                                 ("撮合时可卖持仓不足", o["id"]))
                    skipped.append({"order_id": o["id"], "reason": "撮合时可卖持仓不足"})
                    continue
            _fill(conn, account_id, o["symbol"], o["direction"], o["qty"],
                  o["price"], o["order_type"], trade_date, _now())
            filled.append({"order_id": o["id"], "symbol": o["symbol"],
                           "direction": o["direction"], "qty": o["qty"],
                           "price": o["price"]})
        return {"filled": filled, "skipped": skipped}


def get_account_summary(account_id: str, db_path: str | None = None) -> dict:
    """账户汇总：现金 / 持仓市值 / 总资产 / 盈亏。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        acc = _ensure_account(conn, account_id)
        positions = conn.execute(
            "SELECT * FROM positions WHERE account_id=? AND qty>0",
            (account_id,),
        ).fetchall()
        market_value = 0.0
        unrealized = 0.0
        pos_list = []
        for p in positions:
            px = p["last_price"] or p["avg_cost"]
            mv = px * p["qty"]
            market_value += mv
            unrealized += (px - p["avg_cost"]) * p["qty"]
            sellable = p["qty"] - (
                p["day_bought_qty"] if p["last_buy_date"] == _today() else 0
            )
            pos_list.append(
                {
                    "symbol": p["symbol"],
                    "qty": p["qty"],
                    "sellable": sellable,
                    "avg_cost": p["avg_cost"],
                    "last_price": p["last_price"],
                    "market_value": round(mv, 2),
                    "unrealized_pnl": round((px - p["avg_cost"]) * p["qty"], 2),
                    "pnl_pct": round((px - p["avg_cost"]) / p["avg_cost"] * 100, 2)
                    if p["avg_cost"] else 0.0,
                }
            )
        total = acc["cash"] + market_value
        return {
            "account_id": account_id,
            "name": acc["name"],
            "initial_cash": acc["initial_cash"],
            "cash": round(acc["cash"], 2),
            "market_value": round(market_value, 2),
            "total_equity": round(total, 2),
            "unrealized_pnl": round(unrealized, 2),
            "total_pnl": round(total - acc["initial_cash"], 2),
            "total_return_pct": round((total - acc["initial_cash"]) / acc["initial_cash"] * 100, 2),
            "positions": pos_list,
        }


def get_positions(account_id: str, db_path: str | None = None) -> dict:
    """查询持仓明细。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        rows = conn.execute(
            "SELECT * FROM positions WHERE account_id=? AND qty>0 ORDER BY symbol",
            (account_id,),
        ).fetchall()
        return {"positions": [dict(r) for r in rows]}


def get_orders(
    account_id: str,
    status: str | None = None,
    limit: int = 50,
    db_path: str | None = None,
) -> dict:
    """查询委托记录。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        if status:
            rows = conn.execute(
                "SELECT * FROM orders WHERE account_id=? AND status=? ORDER BY id DESC LIMIT ?",
                (account_id, status, limit),
            ).fetchall()
        else:
            rows = conn.execute(
                "SELECT * FROM orders WHERE account_id=? ORDER BY id DESC LIMIT ?",
                (account_id, limit),
            ).fetchall()
        return {"orders": [dict(r) for r in rows]}


def mark_to_market(
    account_id: str,
    price_map: dict[str, float] | None = None,
    db_path: str | None = None,
) -> dict:
    """按最新价重估持仓（price_map 缺省时尝试实时行情源）。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        positions = conn.execute(
            "SELECT * FROM positions WHERE account_id=? AND qty>0",
            (account_id,),
        ).fetchall()
        symbols = [p["symbol"] for p in positions]
        prices: dict[str, float] = dict(price_map or {})
        missing = [s for s in symbols if s not in prices]
        if missing:
            try:
                from .datasource import fetch_realtime_quotes

                quotes = fetch_realtime_quotes(missing)
                prices.update({q["symbol"]: q["price"] for q in quotes if q["price"] > 0})
            except Exception:  # noqa: BLE001
                pass
        for p in positions:
            px = prices.get(p["symbol"])
            if px:
                conn.execute(
                    "UPDATE positions SET last_price=? WHERE account_id=? AND symbol=?",
                    (px, account_id, p["symbol"]),
                )
        conn.commit()
        return get_account_summary(account_id, db_path=db_path)


def daily_settle(
    account_id: str,
    price_map: dict[str, float] | None = None,
    trade_date: str | None = None,
    db_path: str | None = None,
) -> dict:
    """日终结算：按收盘价重估 → 写当日净值快照 → 解冻 T+1 仓位。"""
    trade_date = trade_date or _today()
    summary = mark_to_market(account_id, price_map, db_path=db_path)
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        # 解冻当日买入（进入新交易日）
        conn.execute(
            "UPDATE positions SET day_bought_qty=0, last_buy_date=NULL WHERE account_id=?",
            (account_id,),
        )
        conn.execute(
            """INSERT OR REPLACE INTO daily_snapshots
               (account_id, trade_date, cash, market_value, total_equity,
                unrealized_pnl, created_at)
               VALUES (?,?,?,?,?,?,?)""",
            (account_id, trade_date, summary["cash"], summary["market_value"],
             summary["total_equity"], summary["unrealized_pnl"], _now()),
        )
        conn.commit()
    summary["trade_date"] = trade_date
    summary["note"] = f"{trade_date} 日终结算完成，T+1 已解冻"
    return summary


def get_equity_curve(account_id: str, db_path: str | None = None) -> dict:
    """净值曲线（日终快照序列）。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        rows = conn.execute(
            "SELECT * FROM daily_snapshots WHERE account_id=? ORDER BY trade_date",
            (account_id,),
        ).fetchall()
        return {"points": [dict(r) for r in rows]}


def close_account(account_id: str, db_path: str | None = None) -> dict:
    """关闭模拟盘账户（保留历史数据）。"""
    with closing(_connect(db_path or default_db_path())) as conn:
        _init_schema(conn)
        _ensure_account(conn, account_id)
        conn.execute("UPDATE accounts SET closed_at=? WHERE id=?",
                     (_now(), account_id))
        conn.commit()
    return {"account_id": account_id, "closed": True}
