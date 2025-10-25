#!/usr/bin/env bash
set -euo pipefail

# 상태: 실행 여부/현재 PID 표시

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
cd "$ROOT_DIR"

PID_FILE=.run/chess-bot.pid
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

repo_pids=( $(find_repo_pids) )
pf_pid=""; [[ -f "$PID_FILE" ]] && pf_pid="$(cat "$PID_FILE" 2>/dev/null || true)"

if (( ${#repo_pids[@]} == 0 )); then
  echo "[status] not running"
  exit 1
fi

out="[status] running:"
for p in "${repo_pids[@]}"; do
  if [[ "$p" == "$pf_pid" ]]; then
    out+=" pid=$p[*]"
  else
    out+=" pid=$p"
  fi
done
echo "$out"
exit 0
