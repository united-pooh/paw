#!/usr/bin/env bash
# Regenerates internal/model/metadata/context_windows.json from
# basellm/llm-metadata's built API (dist/api/all.json).
#
# Usage: scripts/gen-llm-metadata.sh [all.json] [output.json]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INPUT="${1:-}"
OUTPUT="${2:-$ROOT/internal/model/metadata/context_windows.json}"

if [[ -z "$INPUT" ]]; then
    TMP="$(mktemp -d)"
    trap 'rm -rf "$TMP"' EXIT
    INPUT="$TMP/all.json"
    echo "fetching llm-metadata dist/api/all.json ..." >&2
    curl -fsSL --max-time 60 \
        -o "$INPUT" \
        "https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json"
fi

mkdir -p "$(dirname "$OUTPUT")"
python3 - "$INPUT" "$OUTPUT" <<'PY'
import json
import sys

src, dst = sys.argv[1], sys.argv[2]

with open(src, encoding="utf-8") as fh:
    data = json.load(fh)

# Aggregate by lowercased model id; when the same model name exists under
# multiple providers (e.g. alibaba vs alibaba-cn), keep the largest context
# window so we never underestimate a model's real limit.
windows = {}
for provider, entry in data.items():
    if not isinstance(entry, dict):
        continue
    models = entry.get("models")
    if isinstance(models, dict):
        models = list(models.values())
    if not isinstance(models, list):
        continue
    for model in models:
        if not isinstance(model, dict):
            continue
        model_id = str(model.get("id", "")).strip()
        limit = model.get("limit")
        if not model_id or not isinstance(limit, dict):
            continue
        context = limit.get("context")
        if not isinstance(context, int) or context <= 0:
            continue
        key = model_id.lower()
        if key not in windows or context > windows[key]:
            windows[key] = context

with open(dst, "w", encoding="utf-8") as fh:
    json.dump({
        "source": "https://github.com/basellm/llm-metadata",
        "contextWindows": dict(sorted(windows.items())),
    }, fh, indent=2, ensure_ascii=False)
    fh.write("\n")

print(f"wrote {len(windows)} context windows to {dst}")
PY
