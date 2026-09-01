#!/usr/bin/env bash
set -euo pipefail
seed_dir="$(mktemp -d)/fixture-workspace"
mkdir -p "$seed_dir"
go run ./internal/web/testdata/webfixture -port 18777 -workspace "$seed_dir" > /tmp/paw-webfixture.log 2>&1 &
fixture_pid=$!
trap 'kill "$fixture_pid" 2>/dev/null || true' EXIT
# Wait for the fixture to print its one-time bootstrap URL.
for _ in $(seq 1 60); do
  grep -q 'bootstrap=' /tmp/paw-webfixture.log 2>/dev/null && break
  sleep 0.5
done
wait "$fixture_pid"
