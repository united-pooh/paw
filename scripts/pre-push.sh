#!/usr/bin/env bash
# ============================================================
# pre-push hook: 推送 dev 分支时，自动构建并安装 `paw` 到 ~/go/bin
#
# 安装:  cp scripts/pre-push.sh .git/hooks/pre-push && chmod +x .git/hooks/pre-push
# 或运行: ./scripts/install-pre-push.sh
#
# 行为:
#   - 仅当推送目标是 refs/heads/dev 时才构建安装，其他分支直接放行
#   - 构建产物: go build -trimpath -ldflags "-s -w" ./cmd/agent -> ~/go/bin/paw
#   - 若 HEAD 不是 dev，则用临时 worktree 构建被推送的 dev 快照，保证与推送内容一致
#   - 构建失败会中止本次 push（相当于 release gate），可用 exit 0 取消
# ============================================================
set -euo pipefail

WANT="refs/heads/dev"
building=0
while read -r lref lsha rref rsha; do
  [[ "$lref" == "$WANT" ]] && building=1
done

# 非 dev 分支推送：放行，不构建
if [[ "$building" == "0" ]]; then
  exit 0
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_DIR="${GOBIN:-$HOME/go/bin}"
BIN="$INSTALL_DIR/paw"
mkdir -p "$INSTALL_DIR"

sha="$(git -C "$ROOT" rev-parse refs/heads/dev 2>/dev/null || true)"
head="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)"
short="${sha:0:8}"

if [[ -z "$sha" ]]; then
  echo "pre-push: 未找到 refs/heads/dev，跳过安装" >&2
  exit 0
fi

echo "==> pre-push: 推送 dev (${short})，构建并安装 paw -> ${BIN}"

build_in() {
  local dir="$1"
  ( cd "$dir" && go build -trimpath -ldflags "-s -w" -o "$BIN.tmp" ./cmd/agent ) \
    && mv -f "$BIN.tmp" "$BIN"
}

if [[ "$head" == "$sha" ]]; then
  # 当前就在 dev 上：直接用工作树构建（享受 Go 缓存，最快）
  build_in "$ROOT"
else
  # 不在 dev 上：用临时 worktree 构建 dev 快照，保证二进制 = 推送的内容
  TMP="$(mktemp -d "${TMPDIR:-/tmp}/paw-hook.XXXXXX")"
  cleanup() {
    git -C "$ROOT" worktree remove --force "$TMP" >/dev/null 2>&1 || rm -rf "$TMP"
  }
  trap cleanup EXIT
  if ! git -C "$ROOT" worktree add --detach "$TMP" refs/heads/dev >/dev/null 2>&1; then
    echo "pre-push: 无法为 dev 创建临时 worktree（可能正被其他 worktree 检出），跳过安装" >&2
    exit 0
  fi
  build_in "$TMP"
fi

echo "==> paw 已安装: ${BIN} ($(ls -lh "$BIN" | awk '{print $5}'))"
