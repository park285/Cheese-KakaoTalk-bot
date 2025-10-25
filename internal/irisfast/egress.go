package irisfast

import (
    "context"
    "errors"
    "time"

    "go.uber.org/zap"
    "nhooyr.io/websocket/wsjson"
)

// Egress abstracts message/image sending over HTTP or WebSocket.
type Egress interface {
    SendText(ctx context.Context, room, message string) error
    SendImage(ctx context.Context, room, imageBase64 string) error
}

type transportMode string

const (
    transportHTTP transportMode = "http"
    transportWS   transportMode = "ws"
    transportAuto transportMode = "auto"
)

// NewEgress creates an Egress based on mode. When mode is auto, WS is preferred when connected;
// on WS failure, it falls back to HTTP once.
func NewEgress(mode string, dryrun bool, c *Client, ws *WebSocket, logger *zap.Logger) Egress {
    if logger == nil {
        logger = zap.NewNop()
    }
    m := transportMode(mode)
    switch m {
    case transportWS:
        return &wsEgress{ws: ws, dryrun: dryrun, logger: logger}
    case transportAuto:
        return &autoEgress{ws: &wsEgress{ws: ws, dryrun: dryrun, logger: logger}, http: &httpEgress{c: c}, logger: logger}
    default:
        return &httpEgress{c: c}
    }
}

// httpEgress delegates to Client.
type httpEgress struct{ c *Client }

func (h *httpEgress) SendText(ctx context.Context, room, message string) error {
    if h == nil || h.c == nil { return errors.New("http egress not available") }
    return h.c.SendMessage(ctx, room, message)
}
func (h *httpEgress) SendImage(ctx context.Context, room, imageBase64 string) error {
    if h == nil || h.c == nil { return errors.New("http egress not available") }
    return h.c.SendImage(ctx, room, imageBase64)
}

// wsEgress writes ReplyRequest frames over WebSocket.
type wsEgress struct{
    ws *WebSocket
    dryrun bool
    logger *zap.Logger
}

func (w *wsEgress) SendText(ctx context.Context, room, message string) error {
    if w == nil || w.ws == nil { return errors.New("ws egress not available") }
    if w.dryrun {
        w.logger.Info("ws_egress_dryrun", zap.String("type", "text"), zap.String("room", room))
        return nil
    }
    req := ReplyRequest{Type: "text", Room: room, Data: message}
    return w.writeJSON(ctx, &req)
}
func (w *wsEgress) SendImage(ctx context.Context, room, imageBase64 string) error {
    if w == nil || w.ws == nil { return errors.New("ws egress not available") }
    if w.dryrun {
        w.logger.Info("ws_egress_dryrun", zap.String("type", "image"), zap.String("room", room))
        return nil
    }
    req := ReplyRequest{Type: "image", Room: room, Data: imageBase64}
    return w.writeJSON(ctx, &req)
}

func (w *wsEgress) writeJSON(ctx context.Context, v any) error {
    // Quick state/conn check to avoid write on nil
    if w.ws.conn == nil || w.ws.state != WSStateConnected {
        return errors.New("ws not connected")
    }
    // Serialize write via the WebSocket's connection; this method is called sequentially by callers per message
    // and wsjson.Write is not concurrency-safe across goroutines.
    dctx := ctx
    if _, ok := ctx.Deadline(); !ok {
        // bounded deadline to prevent indefinite blocking
        var cancel context.CancelFunc
        dctx, cancel = context.WithTimeout(ctx, 5*time.Second)
        defer cancel()
    }
    // Use wsjson.Write directly; protect against concurrent writes with the WebSocket's stateM
    // Note: we intentionally do not lock stateM for the duration of I/O; we rely on single-threaded call sites.
    return wsjsonWrite(dctx, w.ws, v)
}

// autoEgress prefers WS if available, with single fallback to HTTP.
type autoEgress struct{
    ws   *wsEgress
    http *httpEgress
    logger *zap.Logger
}

func (a *autoEgress) SendText(ctx context.Context, room, message string) error {
    // Try WS first when connected
    if a.ws != nil && a.ws.ws != nil && a.ws.ws.conn != nil && a.ws.ws.state == WSStateConnected {
        if err := a.ws.SendText(ctx, room, message); err == nil { return nil }
        a.logger.Warn("egress_fallback", zap.String("type", "text"), zap.String("room", room))
    }
    return a.http.SendText(ctx, room, message)
}
func (a *autoEgress) SendImage(ctx context.Context, room, imageBase64 string) error {
    if a.ws != nil && a.ws.ws != nil && a.ws.ws.conn != nil && a.ws.ws.state == WSStateConnected {
        if err := a.ws.SendImage(ctx, room, imageBase64); err == nil { return nil }
        a.logger.Warn("egress_fallback", zap.String("type", "image"), zap.String("room", room))
    }
    return a.http.SendImage(ctx, room, imageBase64)
}

// indirection wrapper to call wsjson.Write without exposing it publicly
func wsjsonWrite(ctx context.Context, ws *WebSocket, v any) error {
    // Using wsjson.Write from ws_nhooyr.go's import (same package)
    return wsjson.Write(ctx, ws.conn, v)
}
