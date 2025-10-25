#!/usr/bin/env bash
set -euo pipefail

# 실행: .env(.env.local 우선) 인라인 로드 후 chess-bot 백그라운드 기동

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
cd "$ROOT_DIR"

log() { printf "[run] %s\n" "$*"; }
err() { printf "[run][ERROR] %s\n" "$*" >&2; }

# 실행 락(동시 실행 방지) — flock 사용 가능 시 적용
LOCK_DIR=.run
LOCK_FILE="$LOCK_DIR/chess-bot.lock"
mkdir -p "$LOCK_DIR" logs
if command -v flock >/dev/null 2>&1; then
  exec 9>"$LOCK_FILE"
  if ! flock -n 9; then
    err "다른 run 프로세스가 실행 중입니다(락 획득 실패). 잠시 후 재시도하세요."
    exit 1
  fi
fi

# 1) env 로드(.env.local → .env)
if [[ -f .env.local ]]; then set -a; source .env.local; set +a; log ".env.local 로드"; fi
if [[ -f .env ]]; then set -a; source .env; set +a; log ".env 로드"; fi

# 2) 필수값 검증(값 출력 없이 이름만 표기)
: "${IRIS_BASE_URL:?IRIS_BASE_URL required}"
: "${IRIS_WS_URL:?IRIS_WS_URL required}"
: "${BOT_PREFIX:?BOT_PREFIX required}"
: "${REDIS_URL:?REDIS_URL required}"
: "${DATABASE_URL:?DATABASE_URL required}"

PID_FILE=.run/chess-bot.pid
if [[ -f "$PID_FILE" ]]; then
  oldpid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [[ -n "${oldpid}" ]] && ps -p "$oldpid" >/dev/null 2>&1; then
    err "이미 실행 중(PID=$oldpid)"
    exit 1
  fi
fi

if [[ ! -x bin/chess-bot ]]; then
  err "bin/chess-bot 없음 — 먼저 scripts/build.sh 실행"
  exit 1
fi

# 추가 중복 방지: 같은 리포지토리의 bin/chess-bot 실행 중인지 검사
TARGET_EXE="$ROOT_DIR/bin/chess-bot"
normalize_exe() { # '/path (deleted)' → '/path'
  local s="$1"
  s="${s% (deleted)}"
  printf '%s' "$s"
}

find_repo_pids() {
  local pids=()
  for d in /proc/[0-9]*; do
    [[ -d "$d" ]] || continue
    local pid="${d#/proc/}"
    local exe
    exe="$(readlink -f "$d/exe" 2>/dev/null || true)"
    exe="$(normalize_exe "$exe")"
    if [[ "$exe" == "$TARGET_EXE" ]]; then
      pids+=("$pid")
    fi
  done
  printf '%s\n' "${pids[@]:-}"
}

existing=( $(find_repo_pids) )
if (( ${#existing[@]} > 0 )); then
  err "동일 리포지토리에서 이미 실행 중: ${existing[*]} — scripts/stop.sh --all 후 재시작하세요."
  exit 1
fi

# 3) Iris 사전검증(실패 시 기동 중단)
IRISCHECK_TIMEOUT="${IRISCHECK_TIMEOUT:-5}"
if [[ -x scripts/irischeck.sh ]]; then
  log "Iris 사전검증 실행(--timeout=${IRISCHECK_TIMEOUT})"
  if ! scripts/irischeck.sh --timeout "${IRISCHECK_TIMEOUT}" >/dev/null; then
    err "Iris 사전검증 실패 — 기동 중단"
    exit 1
  fi
  log "Iris 사전검증 OK"
else
  err "scripts/irischeck.sh 없음 — 기동 중단"; exit 1
fi

log "체스 봇 시작"
nohup ./bin/chess-bot >> logs/chess-bot.stdout.log 2>&1 &
echo $! > "$PID_FILE"
log "PID=$(cat "$PID_FILE")"
