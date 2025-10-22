# Kakao Cheese Bot (Chess Bot) – Iris Fast Client

Standalone chess-bot foundation focused on the Iris communication layer.

- HTTP client: fasthttp (JSON over /reply, /decrypt, /config)
- WebSocket: nhooyr.io/websocket (callbacks, reconnect), decoupled from legacy
- No legacy edits; this repo is independent

## Layout
- `internal/irisfast/types.go` – request/response models and message types
- `internal/irisfast/client.go` – fasthttp-based Iris HTTP client
- `internal/irisfast/ws_client.go` – WebSocket client interface
- `internal/irisfast/ws_nhooyr.go` – nhooyr-based WebSocket client (callbacks, reconnect)

## Next
- Add bot bootstrap and command wiring after Iris layer validation
- Add `cmd/irischeck` to verify `/config` and WS connectivity
- Wire Redis/DB/Stockfish once basic messaging is proven
