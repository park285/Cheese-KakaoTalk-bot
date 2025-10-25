#!/usr/bin/env bash
set -euo pipefail

# Iris 연결 사전검증 자동화
# - .env(.env.local 우선) 인라인 로드
# - HTTP /config 응답 확인(curl)
# - WS 포트 접속성 확인(nc 또는 /dev/tcp)
# - --deep 옵션 시 Go irischeck 실행(HTTP+WS 관찰)

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
cd "$ROOT_DIR"

TIMEOUT=5
SKIP_WS=0
DEEP=0

usage() {
  cat <<USAGE
Usage: scripts/irischeck.sh [options]
  --timeout N   HTTP/WS 타임아웃(초), 기본 5
  --skip-ws     WS 사전검증 생략(HTTP만 확인)
  --deep        Go irischeck 실행(추가 관찰)
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --timeout)
      TIMEOUT="${2:-5}"; shift 2;;
    --skip-ws)
      SKIP_WS=1; shift;;
    --deep)
      DEEP=1; shift;;
    -h|--help)
      usage; exit 0;;
    *)
      echo "[irischeck] unknown option: $1" >&2; usage; exit 2;;
  esac
done

log() { printf "[irischeck] %s\n" "$*"; }
err() { printf "[irischeck][ERROR] %s\n" "$*" >&2; }

# 1) env 로드(.env.local → .env)
if [[ -f .env.local ]]; then set -a; source .env.local; set +a; log ".env.local 로드"; fi
if [[ -f .env ]]; then set -a; source .env; set +a; log ".env 로드"; fi

# 2) 필수값 검증
: "${IRIS_BASE_URL:?IRIS_BASE_URL required}"

# 3) HTTP /config 체크
CONFIG_URL="${IRIS_BASE_URL%/}/config"
log "HTTP 확인: GET /config (timeout=${TIMEOUT}s)"
if command -v curl >/dev/null 2>&1; then
  if ! curl -fsS -m "$TIMEOUT" -o /dev/null "$CONFIG_URL"; then
    err "HTTP 사전검증 실패: $CONFIG_URL"
    exit 1
  fi
  log "HTTP OK: $CONFIG_URL"
else
  err "curl 미설치 — HTTP 확인 건너뜀"
fi

# 4) WS 포트 체크(옵션)
if [[ "${SKIP_WS}" -eq 0 ]]; then
  if [[ -n "${IRIS_WS_URL-}" ]]; then
    WS_URL="$IRIS_WS_URL"
    # ws(s)://host:port/path → host, port 추출
    # 기본 포트: ws=80, wss=443
    proto="${WS_URL%%://*}"; rest="${WS_URL#*://}"; hostport="${rest%%/*}"
    host="${hostport%%:*}"
    if [[ "$hostport" == *:* ]]; then port="${hostport##*:}"; else port=$([[ "$proto" == "wss" ]] && echo 443 || echo 80); fi
    log "WS 확인: tcp ${host}:${port} (timeout=${TIMEOUT}s)"
    if command -v nc >/dev/null 2>&1; then
      if ! nc -z -w "$TIMEOUT" "$host" "$port"; then
        err "WS 사전검증 실패: ${host}:${port}"
        exit 1
      fi
      log "WS TCP OK: ${host}:${port}"
    else
      # bash /dev/tcp 폴백
      if ! timeout "$TIMEOUT" bash -c "</dev/tcp/${host}/${port}" 2>/dev/null; then
        err "WS 사전검증 실패: ${host}:${port} (no nc)"
        exit 1
      fi
      log "WS TCP OK(/dev/tcp): ${host}:${port}"
    fi
  else
    log "IRIS_WS_URL 미설정 — WS 확인 생략"
  fi
fi

# 5) Deep 모드: Go irischeck 실행(있으면 바이너리, 없으면 go run)
if [[ "${DEEP}" -eq 1 ]]; then
  if [[ -x bin/irischeck ]]; then
    log "실행: bin/irischeck"
    bin/irischeck || true
  elif command -v go >/dev/null 2>&1; then
    log "실행: go run ./cmd/irischeck"
    go run ./cmd/irischeck || true
  else
    err "irischeck 바이너리/Go 미존재 — deep 모드 건너뜀"
  fi
fi

log "사전검증 완료"

