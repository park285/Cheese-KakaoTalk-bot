#!/usr/bin/env bash
set -euo pipefail

# 재시작: stop → run (.env 로드는 run.sh에서 수행)

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
cd "$ROOT_DIR"

./scripts/stop.sh --all || true
exec ./scripts/run.sh
