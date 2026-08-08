"""quant-mcp MCP 服务器入口：注册全部 Tools / Resources / Prompts。"""

from mcp.server.fastmcp import FastMCP

from .resources.notes import register as register_resources
from .prompts.strategy_review import register as register_prompts
from .tools.agent_tools import register as register_agent_tools
from .tools.aggressive_trend_tools import register as register_aggressive_trend_tools
from .tools.backtest_tools import register as register_backtest_tools
from .tools.autopsy_tools import register as register_autopsy_tools
from .tools.data_tools import register as register_data_tools
from .tools.defensive_tools import register as register_defensive_tools
from .tools.execution_tools import register as register_execution_tools
from .tools.hft_tools import register as register_hft_tools
from .tools.mean_reversion_tools import register as register_mean_reversion_tools
from .tools.regime_tools import register as register_regime_tools
from .tools.sharpe_tools import register as register_sharpe_tools
from .tools.sim_tools import register as register_sim_tools
from .tools.screening_tools import register as register_screening_tools

INSTRUCTIONS = (
    "AI量化交易工具箱（源自《AI量化交易从0到1》+ hftbacktest 方法论）：\n"
    "- 反过拟合：calc_deflated_sharpe / calc_min_sharpe（López de Prado 2014）\n"
    "- 执行：calc_ac_execution（Almgren-Chriss 2000，第19课）\n"
    "- Regime：detect_regime_hmm（手写 Baum-Welch，第12课）\n"
    "- 风控：autopsy_strategy（附录B 12 种死亡方式）\n"
    "- 架构：simulate_multi_agent（第11课多智能体，一票否决对照）\n"
    "- HFT 做市：calibrate_glft_mm（GLFT 最优报价）/ compute_microstructure_signals"
    "（OBI/VAMP/EffVAMP）/ simulate_latency_impact（延迟影响）\n"
    "- 回测：backtest_etf（收盘信号/次日开盘、波动率目标、止损、成本）\n"
    "- 权益防守：defensive_equity_filter（趋势/动量过滤、目标波动率仓位、组合风险门）\n"
    "- 震荡/高波动：mean_reversion_backtest（偏离均值、反弹确认、波动率门控）\n"
    "- 牛市进攻：aggressive_trend_backtest（高参与度、突破确认、宽幅回撤保护）\n"
    "- 数据：fetch_kline / fetch_realtime_quotes / fetch_stock_list / watch_snapshot"
    "（akshare/baostock/tushare 自动回退，免费源）\n"
    "- 模拟盘：create_paper_account / paper_submit_order / paper_daily_settle 等"
    "（SQLite，T+1/整手/费用，永不连接真实下单）\n"
    "学习资料：quant://notes/*（论文勘误地图/死亡方式速查/学习路线/HFT 工具箱/做市速查）\n"
    "提示词：review_strategy（策略体检）/ read_papers（论文阅读）"
)


def build() -> FastMCP:
    """创建并注册全部能力的服务器实例（每次调用返回独立实例）。"""
    mcp = FastMCP("quant-mcp", instructions=INSTRUCTIONS)
    register_sharpe_tools(mcp)
    register_execution_tools(mcp)
    register_regime_tools(mcp)
    register_autopsy_tools(mcp)
    register_agent_tools(mcp)
    register_backtest_tools(mcp)
    register_defensive_tools(mcp)
    register_mean_reversion_tools(mcp)
    register_aggressive_trend_tools(mcp)
    register_hft_tools(mcp)
    register_data_tools(mcp)
    register_sim_tools(mcp)
    register_screening_tools(mcp)
    register_resources(mcp)
    register_prompts(mcp)
    return mcp
