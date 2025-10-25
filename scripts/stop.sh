#!/usr/bin/env bash
set -euo pipefail

# 중지: 기본은 PID 파일 기반 종료, --all 시 동일 리포지토리의 모든 bin/chess-bot 종료

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
cd "$ROOT_DIR"

ALL=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --all) ALL=1; shift;;
    -h|--help)
      echo "Usage: scripts/stop.sh [--all]"; exit 0;;
    *) echo "[stop] unknown option: $1" >&2; exit 2;;
  esac
done

TARGET_EXE="$ROOT_DIR/bin/chess-bot"
PID_FILE=.run/chess-bot.pid

stop_one() {
  local pid="$1"
  if ! ps -p "$pid" >/dev/null 2>&1; then
    echo "[stop] pid=$pid 이미 종료됨"; return 0
  fi
  echo "[stop] 종료 시도(PID=$pid)"
  kill "$pid" || true
  for i in {1..20}; do # 최대 10초 대기(0.5s * 20)
    if ! ps -p "$pid" >/dev/null 2>&1; then
      echo "[stop] 정상 종료(PID=$pid)"; return 0
    fi
    sleep 0.5
  done
  echo "[stop] 강제 종료(SIGKILL) PID=$pid"
  kill -9 "$pid" || true
}

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

if (( ALL == 1 )); then
  pids=( $(find_repo_pids) )
  if (( ${#pids[@]} == 0 )); then
    echo "[stop] 실행 중인 프로세스 없음"; exit 0
  fi
  for p in "${pids[@]}"; do
    stop_one "$p"
  done
  # PID 파일 정리
  rm -f "$PID_FILE"
  echo "[stop] 완료(--all)"; exit 0
fi

# 기본: PID 파일 기반 한 개 종료
if [[ ! -f "$PID_FILE" ]]; then
  echo "[stop] 실행 중인 프로세스 없음"; exit 0
fi

pid="$(cat "$PID_FILE" 2>/dev/null || true)"
if [[ -z "${pid}" ]]; then
  echo "[stop] PID 파일 손상 — 제거"; rm -f "$PID_FILE"; exit 0
fi

stop_one "$pid"
rm -f "$PID_FILE"
echo "[stop] 완료"
