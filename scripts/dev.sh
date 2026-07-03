#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PAW_ROOT="/Users/united_pooh/PyProjects/Paw"
PAW_BUILD="$PAW_ROOT/build"
PAW_APP="$PAW_BUILD/Paw.app"
MODEL_CFG="$GO_ROOT/.ccagent/model.json"

# ── 颜色 ──────────────────────────────────────────────
CYAN='\033[0;36m'; GREEN='\033[0;32m'
YELLOW='\033[1;33m'; RED='\033[0;31m'
BOLD='\033[1m'; NC='\033[0m'

log()  { echo -e "${CYAN}[dev]${NC} $*"; }
ok()   { echo -e "${GREEN}[dev]${NC} $*"; }
warn() { echo -e "${YELLOW}[dev]${NC} $*"; }
die()  { echo -e "${RED}[dev]${NC} $*" >&2; exit 1; }

# ── Ctrl-C 清理 ───────────────────────────────────────
AGENT_PID=""
cleanup() {
    echo ""
    if [[ -n "$AGENT_PID" ]] && kill -0 "$AGENT_PID" 2>/dev/null; then
        log "停止 Agent 服务 (pid $AGENT_PID)..."
        kill "$AGENT_PID" 2>/dev/null || true
    fi
    log "Bye."
}
trap cleanup EXIT INT TERM

# ── 检测 Provider & API Key ───────────────────────────
detect_provider() {
    if [[ -n "${DEEPSEEK_API_KEY:-}" ]]; then
        echo "deepseek"
    elif [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
        echo "anthropic"
    else
        echo ""
    fi
}

PROVIDER=$(detect_provider)
if [[ -z "$PROVIDER" ]]; then
    die "未设置 API Key。请选一个：\n\
  DeepSeek:  export DEEPSEEK_API_KEY=sk-...\n\
  Anthropic: export ANTHROPIC_API_KEY=sk-ant-..."
fi

ok "Provider: $PROVIDER"

# ── 写 model 配置（仅当 provider 与当前配置不同时）────
write_model_cfg() {
    local provider="$1"
    mkdir -p "$(dirname "$MODEL_CFG")"

    case "$provider" in
      deepseek)
        cat > "$MODEL_CFG" << JSON
{
  "provider": "deepseek",
  "api_base_url": "https://api.deepseek.com",
  "api_path": "/chat/completions",
  "api_key_env_name": "DEEPSEEK_API_KEY",
  "model": "deepseek-chat",
  "timeout": 120
}
JSON
        ;;
      anthropic)
        cat > "$MODEL_CFG" << JSON
{
  "provider": "anthropic",
  "api_base_url": "https://api.anthropic.com",
  "api_path": "/v1/messages",
  "api_key_env_name": "ANTHROPIC_API_KEY",
  "model": "claude-opus-4-5-20251101",
  "timeout": 120
}
JSON
        ;;
    esac
    log "已写入 model 配置：$MODEL_CFG"
}

# 检查是否需要更新配置
CURRENT_PROVIDER=""
if [[ -f "$MODEL_CFG" ]]; then
    CURRENT_PROVIDER=$(python3 -c "import json,sys; d=json.load(open('$MODEL_CFG')); print(d.get('provider',''))" 2>/dev/null || true)
fi

if [[ "$CURRENT_PROVIDER" != "$PROVIDER" ]]; then
    write_model_cfg "$PROVIDER"
else
    warn "Model 配置已是 $PROVIDER，跳过覆写"
fi

# ── 构建 Go agent ─────────────────────────────────────
log "构建 Go agent..."
cd "$GO_ROOT"
go build -o /tmp/codex-agent ./cmd/agent/... || die "Go 构建失败"
ok "Go agent 已构建"

# ── 构建 Paw（首次或 --rebuild）─────────────────────
REBUILD=false
[[ "${1:-}" == "--rebuild" ]] && REBUILD=true
[[ ! -d "$PAW_APP" ]] && REBUILD=true

if $REBUILD; then
    log "构建 Paw.app（首次约需 30-60s）..."
    mkdir -p "$PAW_BUILD/DerivedData"
    xcodebuild build \
        -project "$PAW_ROOT/Paw.xcodeproj" \
        -scheme Paw \
        -destination 'platform=macOS' \
        -derivedDataPath "$PAW_BUILD/DerivedData" \
        2>&1 | grep -E "error:|BUILD SUCCEEDED|BUILD FAILED" | tail -5

    FOUND=$(find "$PAW_BUILD/DerivedData" -name "Paw.app" -maxdepth 6 2>/dev/null | head -1)
    [[ -z "$FOUND" ]] && die "Paw 构建失败（找不到 Paw.app）"
    rm -rf "$PAW_APP"
    cp -R "$FOUND" "$PAW_APP"
    ok "Paw.app 已构建"
else
    warn "Paw.app 已存在，跳过构建（用 --rebuild 强制重建）"
fi

# ── 启动 Go agent ─────────────────────────────────────
PORT="${AGENT_WS_PORT:-8765}"
log "启动 Agent 服务 ws://localhost:$PORT ..."
cd "$GO_ROOT"
/tmp/codex-agent &
AGENT_PID=$!

for _ in $(seq 10); do
    nc -z localhost "$PORT" 2>/dev/null && break
    sleep 0.5
done
nc -z localhost "$PORT" 2>/dev/null || die "Agent 服务启动超时（port $PORT）"
ok "Agent 服务已就绪"

# ── 打开 Paw ─────────────────────────────────────────
open "$PAW_APP"
ok "Paw 已打开"

echo ""
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "  Provider  ${BOLD}${PROVIDER}${NC}"
echo -e "  Agent     ws://localhost:${PORT}"
echo -e "  Paw       绿点 = 已连接  │  Ctrl-C = 退出"
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

wait "$AGENT_PID"
