#!/usr/bin/env bash
set -euo pipefail

# 빌드: 바이너리 생성 (bin/chess-bot, bin/irischeck)

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
cd "$ROOT_DIR"

mkdir -p bin

echo "[build] go mod tidy"
go mod tidy

echo "[build] go build ./cmd/chess-bot -> bin/chess-bot"
go build -o bin/chess-bot ./cmd/chess-bot

echo "[build] go build ./cmd/irischeck -> bin/irischeck"
go build -o bin/irischeck ./cmd/irischeck

echo "[build] 완료"

