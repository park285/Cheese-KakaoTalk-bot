package irisfast

import (
    "context"
    "net/http"
    "strings"
    "sync"
    "time"

    "nhooyr.io/websocket"
    "nhooyr.io/websocket/wsjson"
)

type callbackEntry struct {
    id       int
    callback MessageCallback
}

type stateCallbackEntry struct {
    id       int
    callback StateCallback
}

type WebSocket struct {
    wsURL string

	conn   *websocket.Conn
	state  WebSocketState
	stateM sync.RWMutex

	msgCbs   []callbackEntry
	stateCbs []stateCallbackEntry
	cbM      sync.RWMutex

	reconnectAttempts    int
	maxReconnectAttempts int
	reconnectDelay       time.Duration

	pingInterval time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	rootCtx    context.Context
	rootCancel context.CancelFunc

    // optional: inject headers at handshake (e.g., X-User-*)
    headerProvider HeaderProvider
}

func NewWebSocket(wsURL string, maxReconnectAttempts int, reconnectDelay time.Duration) *WebSocket {
	return &WebSocket{
		wsURL:                wsURL,
		state:                WSStateDisconnected,
		maxReconnectAttempts: maxReconnectAttempts,
		reconnectDelay:       reconnectDelay,
		pingInterval:         30 * time.Second,
		stopCh:               make(chan struct{}),
		msgCbs:               make([]callbackEntry, 0),
		stateCbs:             make([]stateCallbackEntry, 0),
	}
}

func (ws *WebSocket) Connect(ctx context.Context) error {
	ws.stateM.Lock()
	if ws.state == WSStateConnected || ws.state == WSStateConnecting {
		ws.stateM.Unlock()
		return nil
	}
	ws.stateM.Unlock()

	ws.rootCtx, ws.rootCancel = context.WithCancel(context.Background())
	ws.setState(WSStateConnecting)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

    conn, _, err := websocket.Dial(dialCtx, ws.wsURL, &websocket.DialOptions{
        CompressionMode: websocket.CompressionNoContextTakeover,
        HTTPHeader:       ws.buildHeaders(),
    })
	if err != nil {
		ws.setState(WSStateFailed)
		ws.scheduleReconnect()
		return err
	}

	ws.conn = conn
	ws.reconnectAttempts = 0
	ws.setState(WSStateConnected)

	ws.wg.Add(2)
	go ws.listen()
	go ws.pingLoop()
	return nil
}

func (ws *WebSocket) listen() {
	defer ws.wg.Done()
	for {
		select {
		case <-ws.stopCh:
			return
		default:
		}

		if ws.conn == nil {
			return
		}
		var msg Message
		if err := wsjson.Read(ws.rootCtx, ws.conn, &msg); err != nil {
			if ws.isStopping() {
				return
			}
			ws.setState(WSStateDisconnected)
			_ = ws.closeConn(websocket.StatusGoingAway, "reconnect")
			ws.scheduleReconnect()
			return
		}

		ws.cbM.RLock()
		callbacks := make([]callbackEntry, len(ws.msgCbs))
		copy(callbacks, ws.msgCbs)
		ws.cbM.RUnlock()
		for _, entry := range callbacks {
			if entry.callback != nil {
				entry.callback(&msg)
			}
		}
	}
}

func (ws *WebSocket) pingLoop() {
    defer ws.wg.Done()
    t := time.NewTicker(ws.pingInterval)
    defer t.Stop()
    consecutivePingFailures := 0
    for {
        select {
        case <-ws.stopCh:
            return
        case <-t.C:
            if ws.conn == nil {
                continue
            }
            ctx, cancel := context.WithTimeout(ws.rootCtx, 3*time.Second)
            err := ws.conn.Ping(ctx)
            cancel()
            if err != nil {
                consecutivePingFailures++
                if consecutivePingFailures >= 2 {
                    if ws.isStopping() {
                        return
                    }
                    ws.setState(WSStateDisconnected)
                    _ = ws.closeConn(websocket.StatusGoingAway, "ping failure")
                    ws.scheduleReconnect()
                    consecutivePingFailures = 0
                }
                continue
            }
            // success
            consecutivePingFailures = 0
        }
    }
}

func (ws *WebSocket) scheduleReconnect() {
	if ws.maxReconnectAttempts <= 0 {
		return
	}
	ws.setState(WSStateReconnecting)

	go func() {
		for attempt := 1; attempt <= ws.maxReconnectAttempts; attempt++ {
			select {
			case <-ws.stopCh:
				return
			case <-time.After(backoffDuration(attempt)):
			}

        dialCtx, cancel := context.WithTimeout(ws.rootCtx, 10*time.Second)
        conn, _, err := websocket.Dial(dialCtx, ws.wsURL, &websocket.DialOptions{
            CompressionMode: websocket.CompressionNoContextTakeover,
            HTTPHeader:       ws.buildHeaders(),
        })
        cancel()
        if err != nil {
            continue
        }

        ws.conn = conn
        ws.reconnectAttempts = 0
        ws.setState(WSStateConnected)

        ws.wg.Add(2)
        go ws.listen()
        go ws.pingLoop()
        return
		}
		ws.setState(WSStateFailed)
	}()
}

func (ws *WebSocket) OnMessage(cb MessageCallback) int {
	ws.cbM.Lock()
	defer ws.cbM.Unlock()
	id := len(ws.msgCbs) + 1
	ws.msgCbs = append(ws.msgCbs, callbackEntry{id: id, callback: cb})
	return id
}

func (ws *WebSocket) RemoveMessageCallback(id int) {
	ws.cbM.Lock()
	defer ws.cbM.Unlock()
	for i, cb := range ws.msgCbs {
		if cb.id == id {
			ws.msgCbs = append(ws.msgCbs[:i], ws.msgCbs[i+1:]...)
			break
		}
	}
}

func (ws *WebSocket) OnStateChange(cb StateCallback) int {
	ws.cbM.Lock()
	defer ws.cbM.Unlock()
	id := len(ws.stateCbs) + 1
	ws.stateCbs = append(ws.stateCbs, stateCallbackEntry{id: id, callback: cb})
	return id
}

func (ws *WebSocket) RemoveStateCallback(id int) {
	ws.cbM.Lock()
	defer ws.cbM.Unlock()
	for i, cb := range ws.stateCbs {
		if cb.id == id {
			ws.stateCbs = append(ws.stateCbs[:i], ws.stateCbs[i+1:]...)
			break
		}
	}
}

func (ws *WebSocket) setState(state WebSocketState) {
	ws.stateM.Lock()
	ws.state = state
	ws.stateM.Unlock()

	ws.cbM.RLock()
	callbacks := make([]stateCallbackEntry, len(ws.stateCbs))
	copy(callbacks, ws.stateCbs)
	ws.cbM.RUnlock()
	for _, entry := range callbacks {
		if entry.callback != nil {
			entry.callback(state)
		}
	}
}

func (ws *WebSocket) Close(ctx context.Context) error {
	ws.stopOnce.Do(func() { close(ws.stopCh) })
	_ = ws.closeConn(websocket.StatusNormalClosure, "close")

	done := make(chan struct{})
	go func() {
		ws.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		if ws.rootCancel != nil {
			ws.rootCancel()
		}
		return nil
	}
}

func (ws *WebSocket) closeConn(code websocket.StatusCode, reason string) error {
    if ws.conn == nil {
        return nil
    }
    defer func() { ws.conn = nil }()
    return ws.conn.Close(code, reason)
}

func (ws *WebSocket) isStopping() bool {
    select {
    case <-ws.stopCh:
        return true
    default:
        return false
    }
}

// SetHeaderProvider allows injecting headers into the WS handshake.
func (ws *WebSocket) SetHeaderProvider(h HeaderProvider) {
    ws.headerProvider = h
}

func (ws *WebSocket) buildHeaders() http.Header {
    hdr := http.Header{}
    if ws.headerProvider == nil {
        return hdr
    }
    for k, v := range ws.headerProvider() {
        if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
            continue
        }
        hdr.Set(k, v)
    }
    return hdr
}
