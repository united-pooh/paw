#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PAW_ROOT="/Users/united_pooh/PyProjects/Paw"
PAW_BUILD="$PAW_ROOT/build"
PAW_APP="$PAW_BUILD/Paw.app"
MODEL_CFG="$GO_ROOT/.ccagent/model.json"

CYAN='\033[0;36m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; BOLD='\033[1m'; NC='\033[0m'
log()  { echo -e "${CYAN}[dev]${NC} $*"; }
ok()   { echo -e "${GREEN}[dev]${NC} $*"; }
warn() { echo -e "${YELLOW}[dev]${NC} $*"; }
die()  { echo -e "${RED}[dev]${NC} $*" >&2; exit 1; }

AGENT_PID=""
cleanup() {
    echo ""
    if [[ -n "$AGENT_PID" ]] && kill -0 "$AGENT_PID" 2>/dev/null; then
        log "停止 Agent 服务..."
        kill "$AGENT_PID" 2>/dev/null || true
    fi
    log "Bye."
}
trap cleanup EXIT INT TERM

load_longcat_api_key() {
    if [[ -n "${LONGCAT_API_KEY:-}" ]]; then
        return
    fi
    if command -v zsh >/dev/null 2>&1 && [[ -f "$HOME/.zshrc" ]]; then
        local value
        value="$(zsh -lic 'printf "%s" "${LONGCAT_API_KEY:-}"' 2>/dev/null || true)"
        if [[ -n "$value" ]]; then
            export LONGCAT_API_KEY="$value"
        fi
    fi
}

load_longcat_api_key

# 检测 provider
if [[ -n "${LONGCAT_API_KEY:-}" ]]; then
    PROV="longcat"
elif [[ -n "${DEEPSEEK_API_KEY:-}" ]]; then
    PROV="deepseek"
elif [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
    PROV="anthropic"
else
    die "未设置 API Key。请选一个:\n  LongCat:   export LONGCAT_API_KEY=...\n  DeepSeek:  export DEEPSEEK_API_KEY=sk-...\n  Anthropic: export ANTHROPIC_API_KEY=sk-ant-..."
fi

ok "Provider: ${PROV}"

# 写 model 配置
CURRENT_PROV=""
if [[ -f "$MODEL_CFG" ]]; then
    CURRENT_PROV=$(python3 -c "
import json
d=json.load(open('$MODEL_CFG'))
base=d.get('api_base_url','')
model=d.get('model','')
key=d.get('api_key_env_name','')
provider=d.get('provider','')
if key == 'LONGCAT_API_KEY' and base.rstrip('/') == 'https://api.longcat.chat/openai' and model == 'LongCat-2.0':
    print('longcat')
else:
    print(provider)
" 2>/dev/null || echo "")
fi

if [[ "$CURRENT_PROV" != "$PROV" ]]; then
    mkdir -p "$(dirname "$MODEL_CFG")"
    if [[ "$PROV" == "longcat" ]]; then
        python3 -c "
import json
cfg = {
    'provider': 'custom',
    'api_base_url': 'https://api.longcat.chat/openai',
    'api_path': '/chat/completions',
    'api_key_env_name': 'LONGCAT_API_KEY',
    'model': 'LongCat-2.0',
    'timeout_seconds': 120
}
print(json.dumps(cfg, indent=2))
" > "$MODEL_CFG"
    elif [[ "$PROV" == "deepseek" ]]; then
        python3 -c "
import json
cfg = {
    'provider': 'deepseek',
    'api_base_url': 'https://api.deepseek.com',
    'api_path': '/chat/completions',
    'api_key_env_name': 'DEEPSEEK_API_KEY',
    'model': 'deepseek-chat',
    'timeout_seconds': 120
}
print(json.dumps(cfg, indent=2))
" > "$MODEL_CFG"
    else
        python3 -c "
import json
cfg = {
    'provider': 'anthropic',
    'api_base_url': 'https://api.anthropic.com',
    'api_path': '/v1/messages',
    'api_key_env_name': 'ANTHROPIC_API_KEY',
    'model': 'claude-opus-4-5-20251101',
    'timeout_seconds': 120
}
print(json.dumps(cfg, indent=2))
" > "$MODEL_CFG"
    fi
    log "已写入 model 配置 (${PROV})"
else
    warn "Model 配置已是 ${PROV}，跳过覆写"
fi

# 构建 Go agent
log "构建 Go agent..."
cd "$GO_ROOT"
go build -o /tmp/codex-agent ./cmd/agent/... || die "Go 构建失败"
ok "Go agent 已构建"

# 构建 Paw
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
    [[ -z "$FOUND" ]] && die "Paw 构建失败"
    rm -rf "$PAW_APP"
    cp -R "$FOUND" "$PAW_APP"
    ok "Paw.app 已构建"
else
    warn "Paw.app 已存在，跳过构建（--rebuild 强制重建）"
fi

# 启动 Agent
PORT="${AGENT_WS_PORT:-8765}"

# 清理旧进程（端口被占会导致 agent 绑定失败）
OLD_PID=$(lsof -ti :"$PORT" 2>/dev/null || true)
if [[ -n "$OLD_PID" ]]; then
    warn "端口 ${PORT} 被占用 (pid ${OLD_PID})，清理中..."
    kill -9 "$OLD_PID" 2>/dev/null || true
    sleep 0.3
fi

log "启动 Agent ws://localhost:${PORT} ..."
cd "$GO_ROOT"
/tmp/codex-agent &
AGENT_PID=$!

for i in 1 2 3 4 5 6 7 8 9 10; do
    nc -z localhost "$PORT" 2>/dev/null && break
    sleep 0.5
done
nc -z localhost "$PORT" 2>/dev/null || die "Agent 启动超时 (port ${PORT})"
ok "Agent 服务已就绪"

open "$PAW_APP"
ok "Paw 已打开"

echo ""
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "  Provider  ${BOLD}$PROV${NC}"
echo -e "  Agent     ws://localhost:${PORT}"
echo -e "  Paw       绿点=已连接  Ctrl-C=退出"
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

wait "$AGENT_PID"
