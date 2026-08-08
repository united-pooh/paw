"""12 种死亡方式体检（附录B）：输入收益序列，输出 12 项检查结果。"""

import numpy as np

from .deflated_sharpe import deflated_sharpe_ratio
from .stats import annualized_sharpe, max_drawdown, skew_kurt

ANNUAL = 252
DAILY_LOSS_LIMIT = 0.03
DRAWDOWN_LIMIT = 0.20
CRISIS_CORR = 0.80
MAX_SAFE_LEVERAGE_FRAC = 0.5


def _check_data_pollution(rets: np.ndarray) -> dict:
    std = rets.std(ddof=1)
    n_ext = int((np.abs(rets - rets.mean()) > 6 * std).sum())
    n_zero = int((np.abs(rets) < 1e-12).sum())
    issues = []
    if n_ext >= 1:
        issues.append(f"{n_ext} 个 |z|>6 的极端跳变（疑似除权错误/数据断点）")
    if n_zero > len(rets) * 0.05:
        issues.append(f"{n_zero} 天零收益（疑似停牌/缺失填充）")
    return {"id": 1, "name": "数据污染型死亡",
            "status": "❌" if issues else "✅",
            "note": "；".join(issues) if issues else f"无异常（极端值 {n_ext} 个 / 零收益 {n_zero} 天）",
            "fix": "数据质量检查管道 / 多源交叉验证 / 变更告警"}


def _check_overfit(rets: np.ndarray, n_trials: int) -> dict:
    sr = float(rets.mean() / rets.std(ddof=1)) if rets.std(ddof=1) > 0 else 0.0
    T = len(rets)
    var_sr = 1.0 / T
    g3, g4 = skew_kurt(rets)
    dsr = deflated_sharpe_ratio(sr, var_sr, n_trials, T, g3, g4)
    need = None
    if n_trials > 1:
        lo, hi = 0.0, 10.0
        for _ in range(60):
            mid = (lo + hi) / 2
            if deflated_sharpe_ratio(mid, var_sr, n_trials, T, g3, g4) >= 0.95:
                hi = mid
            else:
                lo = mid
        need = round((lo + hi) / 2 * np.sqrt(ANNUAL), 2)
    note = (f"年化夏普 {annualized_sharpe(rets):.2f}，DSR={dsr:.3f}"
            + (f"（试过 {n_trials} 个组合，需年化 {need} 才达标）" if need else ""))
    return {"id": 2, "name": "过拟合型死亡", "status": "❌" if dsr < 0.95 else "✅",
            "note": note, "fix": "严格 OOS / 限制参数 / 对完美回测保持怀疑"}


def _check_regime_drift(rets: np.ndarray) -> dict:
    half = len(rets) // 2
    a, b = rets[:half], rets[half:]
    diff = b.mean() - a.mean()
    se = np.sqrt(a.var(ddof=1) / len(a) + b.var(ddof=1) / len(b))
    z = diff / se if se > 0 else 0.0
    status = "❌" if z < -2.0 else ("⚠️" if z < -1.0 else "✅")
    return {"id": 3, "name": "Regime 漂移型死亡", "status": status,
            "note": (f"前半段年化 {a.mean() * ANNUAL * 100:+.1f}% → 后半段 "
                     f"{b.mean() * ANNUAL * 100:+.1f}%（z={z:+.1f}）"),
            "fix": "Regime Detection 模块 / 滚动相关性监控 / 多策略分散"}


def _check_execution(rets: np.ndarray, slippage_bp: float, turnover: float) -> dict:
    gross_ann = float(rets.mean() * ANNUAL)
    cost_ann = slippage_bp / 10000.0 * turnover * ANNUAL
    if gross_ann <= 0:
        return {"id": 4, "name": "执行失真型死亡", "status": "❌",
                "note": (f"滑点 {slippage_bp}bp×换手 {turnover:.0%} → 年化成本 {cost_ann:.1%}"
                         f"，毛收益为负（{gross_ann:.1%}），成本雪上加霜"),
                "fix": "保守成本假设 / Tick 回测 / 小资金实盘采样"}
    ratio = cost_ann / gross_ann
    status = "❌" if ratio > 0.5 else ("⚠️" if ratio > 0.2 else "✅")
    return {"id": 4, "name": "执行失真型死亡", "status": status,
            "note": (f"滑点 {slippage_bp}bp×换手 {turnover:.0%} → 年化成本 {cost_ann:.1%}"
                     f"，占毛收益 {ratio * 100:.0f}%"),
            "fix": "保守成本假设 / Tick 回测 / 小资金实盘采样"}


def _check_risk_control(rets: np.ndarray) -> dict:
    worst = float(rets.min())
    dd = max_drawdown(rets)
    issues = []
    if worst < -DAILY_LOSS_LIMIT:
        issues.append(f"单日最差 {worst:.1%} 超过 {DAILY_LOSS_LIMIT:.0%} 警戒线")
    if dd > DRAWDOWN_LIMIT:
        issues.append(f"最大回撤 {dd:.1%} 超过 {DRAWDOWN_LIMIT:.0%} 熔断线")
    return {"id": 5, "name": "风控失效型死亡", "status": "❌" if issues else "✅",
            "note": "；".join(issues) if issues else f"单日最差 {worst:.1%} / 最大回撤 {dd:.1%}，均在线内",
            "fix": "风控与策略独立 / 多层风控 / 风控不可绕过 / 定期演练"}


def _check_liquidity() -> dict:
    return {"id": 6, "name": "流动性枯竭型死亡", "status": "N/A",
            "note": "需盘口深度/成交量数据：止损单能否成交、危机时滑点是否失控",
            "fix": "避免单标的集中 / 监控盘口深度 / 流动性压力测试"}


def _check_correlation(multi_asset: np.ndarray | None) -> dict:
    if multi_asset is None or multi_asset.shape[1] < 2:
        return {"id": 7, "name": "相关性飙升型死亡", "status": "N/A",
                "note": "未提供多资产收益矩阵（需 2 列以上）",
                "fix": "压力测试用危机相关性 / 保留真不相关资产"}
    X = multi_asset
    corr_all = np.corrcoef(X.T)
    idx = np.argsort(X.mean(axis=1))[:20]
    corr_crisis = np.corrcoef(X[idx].T)
    off_all = corr_all[np.triu_indices(corr_all.shape[0], 1)]
    off_crisis = corr_crisis[np.triu_indices(corr_crisis.shape[0], 1)]
    ca, cc = float(off_all.mean()), float(off_crisis.mean())
    status = "❌" if cc > CRISIS_CORR else ("⚠️" if cc > 0.6 else "✅")
    return {"id": 7, "name": "相关性飙升型死亡", "status": status,
            "note": f"全期平均相关 {ca:.2f} → 危机窗口 {cc:.2f}",
            "fix": "压力测试用危机相关性 / 保留真不相关资产"}


def _check_leverage(rets: np.ndarray, leverage: float) -> dict:
    dd = max_drawdown(rets)
    safe = 1.0 / dd * MAX_SAFE_LEVERAGE_FRAC if dd > 0 else float("inf")
    return {"id": 8, "name": "杠杆爆仓型死亡",
            "status": "❌" if leverage > safe else "✅",
            "note": (f"杠杆 {leverage:.1f}x vs 回撤 {dd:.1%} 下安全上限 {safe:.1f}x"
                     f"（{dd:.1%}×{leverage:.1f}={leverage * dd:.1%} 本金）"),
            "fix": "杠杆上限<2x / 波动率调整杠杆 / 保证金缓冲 50%"}


def _check_human() -> dict:
    return {"id": 9, "name": "人为干预型死亡", "status": "N/A",
            "note": "需操作日志：手动取消止损 / 亏损加仓 / 覆盖系统信号",
            "fix": "双人确认 / 干预留痕审批 / 干预胜率<50% 禁止干预"}


def _check_system() -> dict:
    return {"id": 10, "name": "系统故障型死亡", "status": "N/A",
            "note": "需运维监控：订单发送失败 / 行情中断 / 延迟",
            "fix": "高可用主备切换 / 健康监控告警 / 安全模式只平仓"}


def _check_regulation() -> dict:
    return {"id": 11, "name": "监管变化型死亡", "status": "N/A",
            "note": "需合规订阅：交易禁令 / 税收 / 保证金变化",
            "fix": "分散策略与地区 / 关注监管动态 / 预留缓冲期"}


def _check_adaptation(rets: np.ndarray) -> dict:
    n = len(rets)
    win = 60
    rolling = np.array([rets[i:i + win].mean() for i in range(n - win)])
    first = rolling[:len(rolling) // 2].mean()
    last = rolling[len(rolling) // 2:].mean()
    se = rolling.std(ddof=1) / np.sqrt(len(rolling) / win)
    z = (last - first) / se if se > 0 else 0.0
    status = "❌" if z < -2.0 else ("⚠️" if z < -1.0 else "✅")
    return {"id": 12, "name": "对手盘适应型死亡", "status": status,
            "note": (f"滚动60日收益：前半 {first * ANNUAL * 100:+.1f}%年化 → 后半 "
                     f"{last * ANNUAL * 100:+.1f}%年化（z={z:+.1f}）"),
            "fix": "分散信号 / 监控容量与边际收益 / 持续研发替换"}


def autopsy(rets: list[float], n_trials: int = 1, slippage_bp: float = 1.0,
            turnover: float = 0.1, leverage: float = 1.0,
            multi_asset: list[list[float]] | None = None) -> dict:
    """完整体检：返回 12 项检查 + 健康度汇总。"""
    arr = np.asarray(rets, dtype=float)
    ma = np.asarray(multi_asset, dtype=float) if multi_asset else None
    checks = [
        _check_data_pollution(arr),
        _check_overfit(arr, n_trials),
        _check_regime_drift(arr),
        _check_execution(arr, slippage_bp, turnover),
        _check_risk_control(arr),
        _check_liquidity(),
        _check_correlation(ma),
        _check_leverage(arr, leverage),
        _check_human(),
        _check_system(),
        _check_regulation(),
        _check_adaptation(arr),
    ]
    na = [c for c in checks if c["status"] == "N/A"]
    ok = [c for c in checks if c["status"] == "✅"]
    bad = [c for c in checks if c["status"] == "❌"]
    warn = [c for c in checks if c["status"] == "⚠️"]
    score = len(ok) / (len(checks) - len(na)) if len(checks) > len(na) else 1.0
    return {
        "checks": checks,
        "health_score": round(score, 3),
        "summary": {
            "ok": len(ok), "warn": len(warn), "bad": len(bad), "na": len(na),
            "critical": [f"#{c['id']} {c['name']}" for c in bad],
        },
    }
