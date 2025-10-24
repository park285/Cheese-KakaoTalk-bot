package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/irisfast"
    "github.com/park285/Cheese-KakaoTalk-bot/internal/obslog"
    "go.uber.org/zap"
)

func main() {
    // 로깅 초기화
    _ = obslog.InitFromEnv()
    logger := obslog.L()
	baseURL := os.Getenv("IRIS_BASE_URL")
	wsURL := os.Getenv("IRIS_WS_URL")

	if baseURL == "" {
		logger.Fatal("iris_base_url_required")
	}

	// Align with legacy: do not inject custom HTTP headers
	client := irisfast.NewClient(baseURL,
		irisfast.WithTimeout(8*time.Second),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, err := client.GetConfig(ctx)
	if err != nil {
		logger.Error("config_error", zap.Error(err))
	} else {
		logger.Info("config_ok", zap.Int("port", cfg.Port), zap.Int("polling", cfg.PollingSpeed), zap.Int("rate", cfg.MessageRate), zap.String("endpoint", cfg.WebserverEndpoint))
	}

	if wsURL == "" {
		logger.Info("ws_url_not_set", zap.String("action", "skip_ws_check"))
		return
	}

    ws := irisfast.NewWebSocket(wsURL, 5, time.Second)
    ws.SetLogger(logger)
    ws.OnStateChange(func(state irisfast.WebSocketState) {
        logger.Info("ws_state_cb", zap.String("state", state.String()))
    })
	ws.OnMessage(func(msg *irisfast.Message) {
		from := "?"
		if msg.Sender != nil {
			from = *msg.Sender
		}
		// PII 최소화: 메시지 본문 미출력
		logger.Info("ws_message", zap.String("room", msg.Room), zap.String("from", from))
		_ = fmt.Sprintf("") // keep fmt import
	})

	cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ccancel()
	if err := ws.Connect(cctx); err != nil {
		logger.Error("ws_connect_error", zap.Error(err))
		return
	}

	// Observe for a short window
	t := time.NewTimer(10 * time.Second)
	<-t.C

	_ = ws.Close(context.Background())
}
