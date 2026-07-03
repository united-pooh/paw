#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PAW_ROOT="/Users/united_pooh/PyProjects/Paw"
PAW_BUILD="$PAW_ROOT/build"
PAW_APP="$PAW_BUILD/Paw.app"

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

# ── 检查 API Key ──────────────────────────────────────
[[ -z "${ANTHROPIC_API_KEY:-}" ]] && \
    die "未设置 ANTHROPIC_API_KEY。\n  请先: export ANTHROPIC_API_KEY=sk-ant-..."

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
    log "构建 Paw.app（首次构建约需 30-60s）..."
    mkdir -p "$PAW_BUILD/DerivedData"
    xcodebuild build \
        -project "$PAW_ROOT/Paw.xcodeproj" \
        -scheme Paw \
        -destination 'platform=macOS' \
        -derivedDataPath "$PAW_BUILD/DerivedData" \
        2>&1 | grep -E "^.*(error:|BUILD SUCCEEDED|BUILD FAILED)" | tail -5

    FOUND=$(find "$PAW_BUILD/DerivedData" -name "Paw.app" -maxdepth 6 2>/dev/null | head -1)
    [[ -z "$FOUND" ]] && die "Paw 构建失败（找不到 Paw.app）"
    rm -rf "$PAW_APP"
    cp -R "$FOUND" "$PAW_APP"
    ok "Paw.app 已构建"
else
    warn "Paw.app 已存在，跳过构建（用 --rebuild 强制重新构建）"
fi

# ── 启动 Go agent ─────────────────────────────────────
PORT="${AGENT_WS_PORT:-8765}"
log "启动 Agent 服务 ws://localhost:$PORT ..."
AGENT_WS_PORT="$PORT" /tmp/codex-agent &
AGENT_PID=$!

# 等待端口就绪（最多 5 秒）
for _ in $(seq 10); do
    nc -z localhost "$PORT" 2>/dev/null && break
    sleep 0.5
done
nc -z localhost "$PORT" 2>/dev/null || die "Agent 服务启动超时（port $PORT 未就绪）"
ok "Agent 服务已就绪"

# ── 打开 Paw ─────────────────────────────────────────
open "$PAW_APP"
ok "Paw 已打开"

echo ""
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "  Agent   ws://localhost:${PORT}"
echo -e "  Paw     绿点 = 已连接  │  Ctrl-C = 退出"
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

wait "$AGENT_PID"
