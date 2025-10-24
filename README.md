# Kakao Cheese Bot (Chess Bot) – Iris Fast Client

Standalone chess-bot foundation focused on the Iris communication layer.

- HTTP client: fasthttp (JSON over /reply, /decrypt, /config)
- WebSocket: nhooyr.io/websocket (callbacks, reconnect), decoupled from legacy
- No legacy edits; this repo is independent

## Policy (의도된 제한)
- HTTP/WS 커스텀 헤더(X-User-*) 미사용: Iris 메시지 페이로드의 식별 정보를 신뢰합니다.
- 시간제(time control) 제거: 파싱/집행/표시 모두 비활성(스키마/환경변수에도 없음).
- 영어 별칭 미제공: `현황`/`보드` 등 한국어 명령만 제공합니다.

## Run (Quick)
- PvP-only (no engine required):
  - export CHESS_PVP_ONLY=true
  - set REDIS_URL, DATABASE_URL
  - go run ./cmd/chess-bot
- Full (with single-player engine):
  - export CHESS_PVP_ONLY=false
  - export STOCKFISH_PATH=/usr/local/bin/stockfish
  - set REDIS_URL, DATABASE_URL
  - go run ./cmd/chess-bot

## Commands

- Note: 영어 별칭(status/board) 미제공 — 한국어 명령(현황/보드)만 지원합니다.

- Prefix: set `BOT_PREFIX` to `!체스` (see `.env.example`).

- Single-player chess
  - `!체스 시작 [level1~level8]`
  - `!체스 e2e4` (SAN/UCI)
  - `!체스 기권`, `!체스 무르기`, `!체스 현황`, `!체스 기록`, `!체스 기보 <ID>`, `!체스 프로필`

- PvP (player vs player)
  - `!체스 방 생성` — 채널 생성(코드 발급)
  - `!체스 방 리스트` — 대기 중인 방 목록(초대 코드 확인)
  - `!체스 참가 <코드>` — 코드로 참가
  - 별칭: `방생성` / `방리스트`(또는 `방목록`) / `방참가 <코드>`
  - `!체스 보드 | 현황` — 현재 PvP 대국 보드/현황 표시
  - `!체스 기권`
  - 수 입력: `!체스 e2e4` 또는 SAN 표기(`Nc6` 등)
  - 색 배정: 항상 랜덤

## Layout
- `internal/irisfast/types.go` – request/response models and message types
- `internal/irisfast/client.go` – fasthttp-based Iris HTTP client
- `internal/irisfast/ws_client.go` – WebSocket client interface
- `internal/irisfast/ws_nhooyr.go` – nhooyr-based WebSocket client (callbacks, reconnect)

## Next
- Add bot bootstrap and command wiring after Iris layer validation
- Add `cmd/irischeck` to verify `/config` and WS connectivity
- Wire Redis/DB/Stockfish once basic messaging is proven
- DB migrations
  - See `db/migrations/*` for optional SQL migrations.
  - (시간제 관련 컬럼은 레거시 정리 단계에서 별도 관리하며, 본 프로젝트 문서에서는 언급하지 않습니다.)

## Logging
- Unified zap logging (console + file)
- Default file: `logs/bot.log` (append-only)
- Legacy-compatible format supported
- Env controls:
  - `LOG_LEVEL` (debug|info|warn|error) — default `info`
  - `LOG_FORMAT` (legacy|console|json) — default `legacy`
  - `LOG_TO_CONSOLE` / `LOG_TO_FILE` — default `true` / `true`
  - `LOG_FILE` — default `logs/bot.log`
  - `LOG_CALLER` — default `false` (legacy 모드에서는 자동 활성화)

### 운영 참고
- Iris 표기가 user로 기록되는 현상: `docs/iris-user-label.md`
