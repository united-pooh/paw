#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root/internal/web/ui"
trap 'rm -rf node_modules tsconfig.app.tsbuildinfo' EXIT
npm ci
npm run lint
npm test
npm run build
