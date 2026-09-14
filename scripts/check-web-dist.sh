#!/usr/bin/env bash
# 检查或记录由 Go embed 的前端 dist 与构建输入是否一致。
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

mode="check"
target=""
case "${1:-}" in
  "") ;;
  --write)
    mode="write"
    target="${2:-}"
    [[ -n "$target" ]] || { echo "usage: $0 --write <frontend-dir>" >&2; exit 2; }
    ;;
  *) echo "usage: $0 [--write <frontend-dir>]" >&2; exit 2 ;;
esac

if [[ "$mode" == "check" && "${PAW_SKIP_WEB_DIST_CHECK:-}" == "1" ]]; then
  echo "check-web-dist: PAW_SKIP_WEB_DIST_CHECK=1，跳过前端产物检查" >&2
  exit 0
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

source_files() {
  local dir="$1" file
  [[ -d "$dir/src" ]] || { echo "check-web-dist: 缺少 $dir/src" >&2; return 1; }
  for file in scripts/check-web-dist.sh \
    "$dir/index.html" "$dir/package.json" "$dir/package-lock.json" \
    "$dir/tsconfig.json" "$dir/tsconfig.app.json" "$dir/vite.config.ts"; do
    [[ -f "$file" ]] || { echo "check-web-dist: 缺少构建输入 $file" >&2; return 1; }
    printf '%s\n' "$file"
  done
  find -L "$dir/src" -type f -print || return 1
  if [[ -e "$dir/public" ]]; then
    find -L "$dir/public" -type f -print || return 1
  fi
  for file in "$dir"/tsconfig.*.json "$dir"/vite.config.* \
    "$dir"/postcss.config.* "$dir"/tailwind.config.* "$dir"/.env*; do
    if [[ -f "$file" ]]; then
      printf '%s\n' "$file"
    fi
  done
}

fingerprint_files() {
  LC_ALL=C sort -u | while IFS= read -r file; do
    printf '%s\0' "$file"
    sha256 < "$file" || return 1
  done | sha256
}

fingerprint() {
  local dir="$1" source_hash dist_hash
  [[ -s "$dir/dist/index.html" ]] || {
    echo "check-web-dist: 缺少或空的 $dir/dist/index.html" >&2
    return 1
  }
  source_hash="$(source_files "$dir" | fingerprint_files)" || return 1
  dist_hash="$(find -L "$dir/dist" -type f ! -name '.paw-source-sha256*' -print | fingerprint_files)" || return 1
  printf 'v2\n%s\n%s\n' "$source_hash" "$dist_hash"
}

check_app() {
  local label="$1" dir="$2"
  local stamp="$dir/dist/.paw-source-sha256"
  local actual expected temporary

  actual="$(fingerprint "$dir")" || {
    echo "  重建: cd $dir && npm run build" >&2
    return 1
  }

  if [[ "$mode" == "write" ]]; then
    temporary="$(mktemp "$stamp.XXXXXX")" || return 1
    if ! printf '%s\n' "$actual" > "$temporary" || ! mv -f "$temporary" "$stamp"; then
      rm -f "$temporary"
      return 1
    fi
    return
  fi

  if [[ ! -f "$stamp" ]]; then
    echo "check-web-dist: ${label} 缺少前端产物指纹 $stamp" >&2
    echo "  重建: cd $dir && npm run build" >&2
    return 1
  fi

  expected="$(cat "$stamp")" || return 1
  if [[ "$expected" != "$actual" ]]; then
    echo "check-web-dist: ${label} 的构建输入或 embed 产物与上次构建不一致" >&2
    echo "  重建: cd $dir && npm run build" >&2
    return 1
  fi
}

failures=0
if [[ "$mode" == "write" ]]; then
  case "$target" in
    internal/ui/web/ui) check_app "工作台" "$target" || failures=$((failures + 1)) ;;
    internal/tokentracer/dashboard) check_app "Token Tracer 看板" "$target" || failures=$((failures + 1)) ;;
    *) echo "check-web-dist: 未知前端目录 $target" >&2; exit 2 ;;
  esac
else
  check_app "工作台" "internal/ui/web/ui" || failures=$((failures + 1))
  check_app "Token Tracer 看板" "internal/tokentracer/dashboard" || failures=$((failures + 1))
fi

if (( failures > 0 )); then
  if [[ "$mode" == "check" ]]; then
    echo "" >&2
    echo "check-web-dist: 前端产物陈旧，已中止构建。" >&2
    echo "  重建后重新执行 make build；确需跳过请设置 PAW_SKIP_WEB_DIST_CHECK=1。" >&2
  fi
  exit 1
fi
