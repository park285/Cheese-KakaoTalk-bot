package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/irisfast"
)

func main() {
	baseURL := os.Getenv("IRIS_BASE_URL")
	wsURL := os.Getenv("IRIS_WS_URL")
	userID := os.Getenv("X_USER_ID")
	userEmail := os.Getenv("X_USER_EMAIL")
	sessionID := os.Getenv("X_SESSION_ID")

	if baseURL == "" {
		log.Fatal("IRIS_BASE_URL is required")
	}

	headers := func() map[string]string {
		m := map[string]string{}
		if userID != "" {
			m["X-User-Id"] = userID
		}
		if userEmail != "" {
			m["X-User-Email"] = userEmail
		}
		if sessionID != "" {
			m["X-Session-Id"] = sessionID
		}
		return m
	}

	client := irisfast.NewClient(baseURL,
		irisfast.WithHeaderProvider(headers),
		irisfast.WithTimeout(8*time.Second),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, err := client.GetConfig(ctx)
	if err != nil {
		log.Printf("/config error: %v", err)
	} else {
		log.Printf("/config ok: port=%d polling=%d rate=%d endpoint=%s", cfg.Port, cfg.PollingSpeed, cfg.MessageRate, cfg.WebserverEndpoint)
	}

	if wsURL == "" {
		log.Println("IRIS_WS_URL not set; skipping WS check")
		return
	}

    ws := irisfast.NewWebSocket(wsURL, 5, time.Second)
    // Propagate headers to WS handshake if needed
    ws.SetHeaderProvider(headers)
    ws.OnStateChange(func(state irisfast.WebSocketState) {
        log.Printf("WS state: %s", state)
    })
	ws.OnMessage(func(msg *irisfast.Message) {
		from := "?"
		if msg.Sender != nil {
			from = *msg.Sender
		}
		fmt.Printf("WS msg room=%s from=%s text=%q\n", msg.Room, from, msg.Msg)
	})

	cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ccancel()
	if err := ws.Connect(cctx); err != nil {
		log.Printf("WS connect error: %v", err)
		return
	}

	// Observe for a short window
	t := time.NewTimer(10 * time.Second)
	<-t.C

	_ = ws.Close(context.Background())
}
