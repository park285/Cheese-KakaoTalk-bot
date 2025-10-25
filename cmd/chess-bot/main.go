package main

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/adapter/chesspresenter"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/chessbuilder"
	appcfg "github.com/park285/Cheese-KakaoTalk-bot/internal/config"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/domain"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/irisfast"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/msgcat"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/obslog"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/pvpchan"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/pvpchess"
	svcchess "github.com/park285/Cheese-KakaoTalk-bot/internal/service/chess"
	"github.com/park285/Cheese-KakaoTalk-bot/pkg/chessdto"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func main() {
	if err := obslog.InitFromEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "log init error: %v\n", err)
	}
	logger := obslog.L()

	cfg, err := appcfg.Load()
	if err != nil {
		logger.Fatal("config_error", zap.Error(err))
	}

	// Align with legacy: do not inject custom HTTP headers
	client := irisfast.NewClient(cfg.IrisBaseURL)
	ws := irisfast.NewWebSocket(cfg.IrisWSURL, 5, time.Second)
	ws.SetLogger(logger)
	ws.OnStateChange(func(state irisfast.WebSocketState) {
		logger.Info("ws_state_cb", zap.String("state", state.String()))
	})

	// PvP chess manager (Redis-backed)
	pvpChessMgr, err := pvpchess.NewManager(cfg.RedisURL)
	if err != nil {
		logger.Fatal("pvp_manager_init_error", zap.Error(err))
	}
	// PvP DB repository
	pvpRepo, err := pvpchess.NewRepository(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("pvp_repo_init_error", zap.Error(err))
	}
	pvpChessMgr.AttachRepository(pvpRepo)

	// Channel lobby manager (Redis-backed)
	addr, pass, db, err := pvpchess.ParseRedisURLForChan(cfg.RedisURL)
	if err != nil {
		logger.Fatal("channel_redis_parse_error", zap.Error(err))
	}
	chanRdb := redis.NewClient(&redis.Options{Addr: addr, Password: pass, DB: db})
	pvpChanMgr := pvpchan.NewManager(chanRdb, pvpChessMgr)

	// 리더 락: 단일 인스턴스 보장 (Redis SET NX + TTL, 주기 갱신)
	{
		lockKey := "bot:leader_lock"
		instanceID := uuid.NewString()
		lockTTL := 20 * time.Second
		ctxLock, cancelLock := context.WithTimeout(context.Background(), 2*time.Second)
		ok, err := chanRdb.SetNX(ctxLock, lockKey, instanceID, lockTTL).Result()
		cancelLock()
		if err != nil {
			logger.Fatal("leader_acquire_error", zap.Error(err))
		}
		if !ok {
			logger.Warn("leader_already_running")
			return
		}
		logger.Info("leader_acquired", zap.String("instance", instanceID))
		// 주기적 TTL 갱신(락 소유자 일치 시에만)
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				ctx := context.Background()
				val, err := chanRdb.Get(ctx, lockKey).Result()
				if err == redis.Nil {
					logger.Warn("leader_lock_lost_nil")
					os.Exit(0)
					return
				}
				if err != nil {
					logger.Warn("leader_refresh_error", zap.Error(err))
					continue
				}
				if strings.TrimSpace(val) != strings.TrimSpace(instanceID) {
					logger.Warn("leader_lock_stolen")
					os.Exit(0)
					return
				}
				_ = chanRdb.Expire(ctx, lockKey, lockTTL).Err()
			}
		}()
		defer func() {
			// 종료 시 소유자 일치하면 해제
			ctx := context.Background()
			if val, _ := chanRdb.Get(ctx, lockKey).Result(); strings.TrimSpace(val) == strings.TrimSpace(instanceID) {
				_ = chanRdb.Del(ctx, lockKey).Err()
			}
			logger.Info("leader_released")
		}()
	}

	// Chess deps (skip engine when PvP-only)
	var deps *chessbuilder.Deps
	if !cfg.PvpOnly {
		d, err := chessbuilder.New(cfg, logger)
		if err != nil {
			logger.Fatal("chess_init_error", zap.Error(err))
		}
		deps = d
	}
	presenter := chesspresenter.NewPresenter(
		func(room, message string) error { return client.SendMessage(context.Background(), room, message) },
		func(room, imageBase64 string) error { return client.SendImage(context.Background(), room, imageBase64) },
	)
	formatter := chesspresenter.NewFormatter(prefixProvider{prefix: cfg.BotPrefix})

	// YAML message catalog (required): no code fallback.
	var catalog *msgcat.Catalog
	cat, cerr := msgcat.New(os.Getenv("TEMPLATE_DIR"))
	if cerr != nil {
		logger.Fatal("msgcat_load_error", zap.Error(cerr))
	}
	catalog = cat
	chesspresenter.SetCatalog(catalog)
	formatter.SetCatalog(catalog)
	logger.Info("msgcat_loaded", zap.Bool("override", strings.TrimSpace(os.Getenv("TEMPLATE_DIR")) != ""))

	// Preflight required keys (YAML-only)
	preflight := []struct {
		key  string
		data map[string]string
	}{
		{"help.korean", map[string]string{"Prefix": cfg.BotPrefix}},
		{"pvp.resign.announce", map[string]string{"ResignerName": "A", "WinnerName": "B"}},
        {"lobby.list.error", nil},
        {"lobby.none", nil},
        {"lobby.make.limit", nil},
        {"lobby.list.header", nil},
		{"lobby.list.item", map[string]string{"Code": "C", "CreatorName": "N"}},
		{"user.identify.error", nil},
		{"pvp.busy.in_room", nil},
		{"pvp.start.announce", map[string]string{"WhiteName": "W", "BlackName": "B"}},
		{"channel.create.error", map[string]string{"Error": "e"}},
		{"usage.lobby", map[string]string{"Prefix": cfg.BotPrefix}},
		{"usage.join", map[string]string{"Prefix": cfg.BotPrefix}},
		{"join.error", map[string]string{"Error": "e"}},
		{"join.waiting", nil},
		{"game.not_found", nil},
		{"render.error", nil},
		{"render.board.failed", nil},
		{"board.send.failed", nil},
		{"no.active.game", nil},
		{"finish.checkmate", map[string]string{"Winner": "W"}},
		{"finish.resign", map[string]string{"Winner": "W"}},
		{"finish.draw", nil},
		{"resign.process.error", nil},
		{"resign.failed", map[string]string{"Error": "e"}},
		{"move.failed", nil},
		{"move.failed_with_error", map[string]string{"Error": "e"}},
		{"move.bad_input", nil},
		{"move.state.error", nil},
		{"chess.start.failed", map[string]string{"Error": "e"}},
		{"chess.status.error", map[string]string{"Error": "e"}},
		{"chess.undo.failed", map[string]string{"Error": "e"}},
		{"chess.history.error", map[string]string{"Error": "e"}},
		{"usage.game", map[string]string{"Prefix": cfg.BotPrefix}},
		{"game.id.invalid", nil},
		{"game.fetch.error", map[string]string{"Error": "e"}},
		{"chess.profile.error", map[string]string{"Error": "e"}},
		{"usage.preset", map[string]string{"Prefix": cfg.BotPrefix}},
		{"chess.preset.update.failed", map[string]string{"Error": "e"}},
		{"chess.assist.failed", map[string]string{"Error": "e"}},
		{"lobby_make.success", map[string]string{"Code": "CODE", "Prefix": cfg.BotPrefix}},
		{"formatter.start.body", map[string]string{"Resumed": "false", "Preset": "level3", "ProfileRatingLine": "• 레이팅: 1200 (▲10)", "ProfileRecordLine": "• 전적: 1승 0패 0무 (1판)", "Prefix": cfg.BotPrefix}},
		{"formatter.status.body", map[string]string{"Preset": "level3", "MoveCount": "10", "RecentLine": "• 최근 e2e4 e7e5", "ProfileInfo": "• 레이팅: 1200", "MaterialLine": "• 잡은 기물 점수 백 +3 / 흑 +0", "CapturedLine": "• 잡은 기물 백 P / 흑 -", "Prefix": cfg.BotPrefix}},
		{"formatter.resign.body", map[string]string{"OutcomeText": "🛑 기권하여 패배로 기록되었습니다.", "ProfileInfo": "• 레이팅: 1200"}},
		{"formatter.move.body", map[string]string{"OutcomeText": "✅ 승리했습니다! 축하드립니다.", "Preset": "level3", "RatingLine": "• 현재 레이팅: 1210 (▲10)", "RecordLine": "• 누적 전적: 1승 0패 0무 (1판)", "GameIDLine": "기보 ID: #1"}},
		{"formatter.undo.body", map[string]string{"Preset": "level3", "MoveCount": "12", "ProfileInfo": "• 레이팅: 1200", "MaterialLine": "• 잡은 기물 점수 백 +1 / 흑 +0", "CapturedLine": "• 잡은 기물 백 P", "Prefix": cfg.BotPrefix}},
		{"formatter.preferred_updated.body", map[string]string{"PreferredPreset": "level3", "ProfileInfo": "• 전적: 10승 5패 2무 (17판)", "Prefix": cfg.BotPrefix}},
		{"formatter.no_session.body", map[string]string{"Prefix": cfg.BotPrefix}},
		{"formatter.history.header", nil},
		{"formatter.history.footer", map[string]string{"Prefix": cfg.BotPrefix}},
		{"formatter.profile.header", nil},
	}
	for _, pf := range preflight {
		if _, err := catalog.Render(pf.key, pf.data); err != nil {
			logger.Fatal("msgcat_preflight_error", zap.String("key", pf.key), zap.Error(err))
		}
	}
	type cachedSender struct {
		name string
		ts   time.Time
	}
	senderCache := make(map[string]cachedSender)
	var senderMu sync.Mutex
	senderTTL := 2 * time.Second
	// Deduplicate identical (room|message) within short window to avoid double-processing
	processed := make(map[string]time.Time)
	var processedMu sync.Mutex
	processedTTL := 2 * time.Second
	ws.OnMessage(func(msg *irisfast.Message) {
		if msg == nil {
			obslog.L().Debug("drop_message", zap.String("reason", "nil"))
			return
		}
		rid := extractRoomID(msg)
		trimmed := messageText(msg)
		if trimmed == "" {
			obslog.L().Debug("drop_message", zap.String("reason", "empty_text"), zap.String("room_id", rid))
			return
		}

		// JSON이 없어도 rid와 메시지가 있으면 처리 가능하도록 개선
		minimalFrame := (msg.JSON == nil || strings.TrimSpace(msg.JSON.RoomID) == "")
		// Debug: frame snapshot
		rawJSONRoom := ""
		rawJSONMsg := ""
		rawJSONChat := ""
		rawSender := ""
		if msg.JSON != nil {
			rawJSONRoom = msg.JSON.RoomID
			rawJSONMsg = msg.JSON.Message
			rawJSONChat = msg.JSON.ChatID
		}
		if msg.Sender != nil {
			rawSender = *msg.Sender
		}
		obslog.L().Debug("ws_frame",
			zap.Bool("minimal", minimalFrame),
			zap.String("room_raw", msg.Room),
			zap.String("room_json", rawJSONRoom),
			zap.String("chat_json", rawJSONChat),
			zap.String("rid", rid),
			zap.String("msg", strings.TrimSpace(msg.Msg)),
			zap.String("json_message", rawJSONMsg),
			zap.String("sender", rawSender),
		)
		if minimalFrame {
			// 캐시: 동일 메시지의 다음 JSON 프레임과 이름 병합을 위한 힌트 유지
			if rid != "" && trimmed != "" && msg.Sender != nil {
				name := strings.TrimSpace(*msg.Sender)
				if name != "" {
					key := rid + "|" + trimmed
					now := time.Now()
					senderMu.Lock()
					for k, v := range senderCache {
						if now.Sub(v.ts) > senderTTL*2 {
							delete(senderCache, k)
						}
					}
					senderCache[key] = cachedSender{name: name, ts: now}
					senderMu.Unlock()
				}
			}
			// rid 없으면 처리 불가
			if rid == "" {
				obslog.L().Debug("drop_message", zap.String("reason", "no_room_id_minimal"))
				return
			}
		}

		displayUser := ""
		if rid != "" && trimmed != "" {
			key := rid + "|" + trimmed
			now := time.Now()
			senderMu.Lock()
			if v, ok := senderCache[key]; ok && now.Sub(v.ts) <= senderTTL {
				displayUser = v.name
			}
			senderMu.Unlock()
		}
		if strings.TrimSpace(displayUser) == "" {
			displayUser = senderName(msg)
		}

		if isIgnoredSender(cfg, displayUser) {
			logger.Debug("drop_message", zap.String("reason", "ignored_sender"), zap.String("sender", strings.TrimSpace(displayUser)))
			return
		}

		// 룸 허용 여부 먼저 필터링
		if len(cfg.AllowedRooms) > 0 && !roomAllowed(cfg.AllowedRooms, msg) {
			logger.Debug("drop_message", zap.String("reason", "room_not_allowed"), zap.String("room_id", extractRoomID(msg)))
			return
		}

		// Prefix 일치 여부에 따라 로그 레벨 분리
		trimmed = messageText(msg)
		sprefix := sanitizeText(cfg.BotPrefix)
		smsg := sanitizeText(trimmed)
		if !strings.HasPrefix(smsg, sprefix) {
			// 최소 로그: room, room_id, user만 기록
			logger.Info(
				"recv_message",
				zap.String("room", msg.Room),
				zap.String("room_id", rid),
				zap.String("user", strings.TrimSpace(displayUser)),
			)
			return
		}

		// Cross-process dedupe: Redis SETNX short TTL to avoid duplicated frames across instances
		if rid != "" && trimmed != "" {
			sh := fmt.Sprintf("%x", sha1.Sum([]byte(smsg)))
			rkey := "dedupe:" + rid + ":" + sh
			if ok, _ := chanRdb.SetNX(context.Background(), rkey, "1", 2*time.Second).Result(); !ok {
				logger.Debug("drop_message", zap.String("reason", "dedupe_redis"), zap.String("room_id", rid))
				return
			}
		}

		// Deduplicate identical messages in a short window to avoid double-processing (minimal+JSON)
		if rid != "" && trimmed != "" {
			k := rid + "|" + smsg
			now := time.Now()
			processedMu.Lock()
			if ts, ok := processed[k]; ok && now.Sub(ts) <= processedTTL {
				processedMu.Unlock()
				logger.Debug("drop_message", zap.String("reason", "dedupe"), zap.String("room_id", rid))
				return
			}
			processed[k] = now
			// cleanup old entries
			for key, t := range processed {
				if now.Sub(t) > processedTTL*2 {
					delete(processed, key)
				}
			}
			processedMu.Unlock()
		}
		// 명령어 토큰 추출: 프리픽스 제거 후 첫 단어
		cmdToken := ""
		{
			raw := strings.TrimSpace(strings.TrimPrefix(smsg, sprefix))
			if raw != "" {
				parts := strings.Fields(raw)
				if len(parts) > 0 {
					cmdToken = strings.ToLower(parts[0])
				}
			}
		}
		// 최소 로그: room, room_id, user, cmd 기록
		logger.Info(
			"recv_message",
			zap.String("room", msg.Room),
			zap.String("room_id", rid),
			zap.String("user", strings.TrimSpace(displayUser)),
			zap.String("cmd", cmdToken),
		)
		var svc *svcchess.Service
		if deps != nil {
			svc = deps.Service
		}
		go handleCommand(client, cfg, pvpChessMgr, pvpChanMgr, svc, presenter, formatter, catalog, msg)
	})

	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := ws.Connect(cctx); err != nil {
		cancel()
		logger.Fatal("ws_connect_error", zap.Error(err))
	}
	cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	_ = ws.Close(context.Background())
	_ = pvpChessMgr.Close()
	_ = pvpRepo.Close()
}

func handleCommand(client *irisfast.Client, cfg *appcfg.AppConfig, pvpChessMgr *pvpchess.Manager, pvpChanMgr *pvpchan.Manager, chess *svcchess.Service, presenter *chesspresenter.Presenter, formatter *chesspresenter.Formatter, catalog *msgcat.Catalog, msg *irisfast.Message) {
	// Parse input using sanitized text to tolerate zero-width/NBSP and JSON-only frames
	msgText := sanitizeText(messageText(msg))
	prefix := sanitizeText(cfg.BotPrefix)
	raw := strings.TrimSpace(strings.TrimPrefix(msgText, prefix))
	if raw == "" {
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Help())
		return
	}
	parts := strings.Fields(raw)
	rawCmd := parts[0]
	cmd := strings.ToLower(rawCmd)
	args := parts[1:]

	switch cmd {
	case "help", "도움":
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Help())
	case "방":
		if len(args) >= 1 {
			sub := strings.ToLower(strings.TrimSpace(args[0]))
			if sub == "리스트" || sub == "목록" {
				metas, err := pvpChanMgr.ListLobby(context.Background())
				if err != nil {
					if txt, err := catalog.Render("lobby.list.error", nil); err == nil {
						_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
					} else {
						obslog.L().Error("msgcat_render_error", zap.String("key", "lobby.list.error"), zap.Error(err))
					}
					return
				}
				if len(metas) == 0 {
					if txt, err := catalog.Render("lobby.none", nil); err == nil {
						_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
					} else {
						obslog.L().Error("msgcat_render_error", zap.String("key", "lobby.none"), zap.Error(err))
					}
					return
				}
				var b strings.Builder
				if header, e := catalog.Render("lobby.list.header", nil); e == nil {
					b.WriteString(header)
				} else {
					b.WriteString("대기 중인 방:")
				}
				b.WriteString("\n")
				for _, m := range metas {
					if item, e := catalog.Render("lobby.list.item", map[string]string{"Code": m.ID, "CreatorName": m.CreatorName}); e == nil {
						b.WriteString(item)
					} else {
						fmt.Fprintf(&b, "• 코드: %s | 만든이: %s", m.ID, m.CreatorName)
					}
					b.WriteString("\n")
				}
				_ = client.SendMessage(context.Background(), extractRoomID(msg), b.String())
				return
			} else if sub == "생성" || sub == "만들기" {
				user := userIDFromMessage(msg)
				if user == "" {
					if txt, err := catalog.Render("user.identify.error", nil); err == nil {
						_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
					} else {
						obslog.L().Error("msgcat_render_error", zap.String("key", "user.identify.error"), zap.Error(err))
					}
					return
				}
				roomID := extractRoomID(msg)
				if g, _ := pvpChessMgr.GetActiveGameByUserInRoom(context.Background(), user, roomID); g != nil {
					if txt, err := catalog.Render("pvp.busy.in_room", nil); err == nil {
						_ = client.SendMessage(context.Background(), roomID, txt)
					} else {
						obslog.L().Error("msgcat_render_error", zap.String("key", "pvp.busy.in_room"), zap.Error(err))
					}
					return
				}
				if chess != nil {
					meta := svcchess.SessionMeta{SessionID: sessionIDFor(msg), Room: roomID, Sender: senderName(msg)}
					if st, err := chess.Status(context.Background(), meta); err == nil && st != nil {
						if txt, e := catalog.Render("pvp.busy.in_room", nil); e == nil {
							_ = client.SendMessage(context.Background(), roomID, txt)
						} else {
							_ = client.SendMessage(context.Background(), roomID, "이미 진행 중인 대국이 있습니다. 종료 후 진행하세요.")
						}
						return
					}
				}
                mr, err := pvpChanMgr.Make(context.Background(), roomID, user, senderName(msg), pvpchan.ColorRandom)
                if err != nil {
                    // 동일 사용자 다중 방 생성 제한 안내
                    if strings.Contains(strings.ToLower(err.Error()), "already has a lobby") {
                        if txt, e := catalog.Render("lobby.make.limit", nil); e == nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), txt) } else { _ = client.SendMessage(context.Background(), extractRoomID(msg), "이미 생성한 대기 방이 있어 새로 만들 수 없습니다.") }
                        return
                    }
                    if txt, e := catalog.Render("channel.create.error", map[string]string{"Error": err.Error()}); e == nil {
                        _ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
                    } else {
                        _ = client.SendMessage(context.Background(), extractRoomID(msg), "채널 생성 실패: "+err.Error())
                    }
                    return
                }
				if txt, err := catalog.Render("lobby_make.success", map[string]string{"Code": mr.Code, "Prefix": cfg.BotPrefix}); err == nil {
					_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
				} else {
					obslog.L().Error("msgcat_render_error", zap.String("key", "lobby_make.success"), zap.Error(err))
				}
				return
			}
		}
		if txt, err := catalog.Render("usage.lobby", map[string]string{"Prefix": cfg.BotPrefix}); err == nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
		} else {
			obslog.L().Error("msgcat_render_error", zap.String("key", "usage.lobby"), zap.Error(err))
		}
		return
	case "참가", "방참가":
		if len(args) < 1 {
			if txt, err := catalog.Render("usage.join", map[string]string{"Prefix": cfg.BotPrefix}); err == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "usage.join"), zap.Error(err))
			}
			return
		}
		code := strings.TrimSpace(args[0])
		user := userIDFromMessage(msg)
		roomID := extractRoomID(msg)
		if g, _ := pvpChessMgr.GetActiveGameByUserInRoom(context.Background(), user, roomID); g != nil {
			if txt, err := catalog.Render("pvp.busy.in_room", nil); err == nil {
				_ = client.SendMessage(context.Background(), roomID, txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "pvp.busy.in_room"), zap.Error(err))
			}
			return
		}
		if chess != nil {
			meta := svcchess.SessionMeta{SessionID: sessionIDFor(msg), Room: roomID, Sender: senderName(msg)}
			if st, err := chess.Status(context.Background(), meta); err == nil && st != nil {
				if txt, err := catalog.Render("pvp.busy.in_room", nil); err == nil {
					_ = client.SendMessage(context.Background(), roomID, txt)
				} else {
					obslog.L().Error("msgcat_render_error", zap.String("key", "pvp.busy.in_room"), zap.Error(err))
				}
				return
			}
		}
		jr, err := pvpChanMgr.Join(context.Background(), extractRoomID(msg), code, user, senderName(msg), pvpchan.ColorRandom)
		if err != nil {
			if txt, e := catalog.Render("join.error", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "join.error"), zap.Error(e))
			}
			return
		}
		if !jr.Started {
			if txt, err := catalog.Render("join.waiting", nil); err == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "join.waiting"), zap.Error(err))
			}
			return
		}
		g, _ := pvpChessMgr.GetActiveGameByUser(context.Background(), user)
		if g == nil {
			if txt, err := catalog.Render("game.not_found", nil); err == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "game.not_found"), zap.Error(err))
			}
			return
		}
        wDTO, wErr := pvpChessMgr.ToDTOForViewer(context.Background(), g, g.WhiteID)
        bDTO, bErr := pvpChessMgr.ToDTOForViewer(context.Background(), g, g.BlackID)
        if wErr != nil || bErr != nil {
            if txt, err := catalog.Render("render.error", nil); err == nil {
                _ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
            } else {
                obslog.L().Error("msgcat_render_error", zap.String("key", "render.error"), zap.Error(err))
            }
            return
        }
        rooms := fanoutRooms(context.Background(), pvpChanMgr, g, extractRoomID(msg))
        // 현재 방 우선 순위 적용
        rooms = prioritizeRooms(rooms, extractRoomID(msg))
        obslog.L().Info("pvp_fanout_targets", zap.Strings("rooms", rooms), zap.String("game_id", g.ID), zap.String("phase", "start"))
        text, _ := catalog.Render("pvp.start.announce", map[string]string{"WhiteName": g.WhiteName, "BlackName": g.BlackName})
        for i, r := range rooms {
            viewer := jr.Meta.CreatorID
            if strings.TrimSpace(r) != strings.TrimSpace(jr.Meta.CreatorRoom) {
                if strings.TrimSpace(viewer) == strings.TrimSpace(g.WhiteID) { viewer = g.BlackID } else { viewer = g.WhiteID }
            }
            vdto := wDTO
            if strings.TrimSpace(viewer) == strings.TrimSpace(g.BlackID) { vdto = bDTO }
            // 1) 텍스트 먼저 전송 (개별 에러 로그)
            if strings.TrimSpace(text) != "" {
                if err := client.SendMessage(context.Background(), r, text); err != nil {
                    obslog.L().Warn("pvp_start_text_error", zap.Error(err), zap.String("room_id", r), zap.String("game_id", g.ID))
                }
            }
            // 2) 짧은 간격 후 이미지만 전송(일부 게이트웨이에서 연속 전송 드롭 방지)
            // 환경값 START_IMAGE_DELAY_MS로 제어(기본 150ms)
            delay := time.Duration(cfg.StartImageDelayMS) * time.Millisecond
            if delay < 0 { delay = 0 }
            time.Sleep(delay)
            if err := presenter.Board(r, "", vdto); err != nil {
                obslog.L().Warn("pvp_board_send_error",
                    zap.Error(err),
                    zap.String("room_id", r),
                    zap.String("game_id", g.ID),
                    zap.String("phase", "start"),
                )
                // 보드 전송 실패 시 보조 안내문 전송
                if t, e := catalog.Render("board.send.failed", nil); e == nil {
                    _ = client.SendMessage(context.Background(), r, t)
                } else {
                    _ = client.SendMessage(context.Background(), r, "보드 전송 실패")
                }
            }
            // 방 간 지연 적용(이미지 드롭 완화)
            if i < len(rooms)-1 {
                d := time.Duration(cfg.FanoutImageDelayMS) * time.Millisecond
                if d > 0 { time.Sleep(d) }
            }
        }
		return
	case "방생성":
		user := userIDFromMessage(msg)
		if user == "" {
			if txt, err := catalog.Render("user.identify.error", nil); err == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "user.identify.error"), zap.Error(err))
			}
			return
		}
		roomID := extractRoomID(msg)
		if g, _ := pvpChessMgr.GetActiveGameByUserInRoom(context.Background(), user, roomID); g != nil {
			if txt, err := catalog.Render("pvp.busy.in_room", nil); err == nil {
				_ = client.SendMessage(context.Background(), roomID, txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "pvp.busy.in_room"), zap.Error(err))
			}
			return
		}
		if chess != nil {
			meta := svcchess.SessionMeta{SessionID: sessionIDFor(msg), Room: roomID, Sender: senderName(msg)}
			if st, err := chess.Status(context.Background(), meta); err == nil && st != nil {
				if txt, err := catalog.Render("pvp.busy.in_room", nil); err == nil {
					_ = client.SendMessage(context.Background(), roomID, txt)
				} else {
					obslog.L().Error("msgcat_render_error", zap.String("key", "pvp.busy.in_room"), zap.Error(err))
				}
				return
			}
		}
mr, err := pvpChanMgr.Make(context.Background(), roomID, user, senderName(msg), pvpchan.ColorRandom)
if err != nil {
    if strings.Contains(strings.ToLower(err.Error()), "already has a lobby") {
        if txt, e := catalog.Render("lobby.make.limit", nil); e == nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), txt) } else { _ = client.SendMessage(context.Background(), extractRoomID(msg), "이미 생성한 대기 방이 있어 새로 만들 수 없습니다.") }
        return
    }
    if txt, e := catalog.Render("channel.create.error", map[string]string{"Error": err.Error()}); e == nil {
        _ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
    } else {
        obslog.L().Error("msgcat_render_error", zap.String("key", "channel.create.error"), zap.Error(e))
    }
    return
}
		if txt, e := catalog.Render("lobby_make.success", map[string]string{"Code": mr.Code, "Prefix": cfg.BotPrefix}); e == nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
		} else {
			obslog.L().Error("msgcat_render_error", zap.String("key", "lobby_make.success"), zap.Error(e))
		}
		return
	case "방리스트", "방목록":
		metas, err := pvpChanMgr.ListLobby(context.Background())
		if err != nil {
			if txt, e := catalog.Render("lobby.list.error", nil); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "lobby.list.error"), zap.Error(e))
			}
			return
		}
		if len(metas) == 0 {
			if txt, e := catalog.Render("lobby.none", nil); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				obslog.L().Error("msgcat_render_error", zap.String("key", "lobby.none"), zap.Error(e))
			}
			return
		}
		var b strings.Builder
		if header, e := catalog.Render("lobby.list.header", nil); e == nil {
			b.WriteString(header)
		} else {
			b.WriteString("대기 중인 방:")
		}
		b.WriteString("\n")
		for _, m := range metas {
			if item, e := catalog.Render("lobby.list.item", map[string]string{"Code": m.ID, "CreatorName": m.CreatorName}); e == nil {
				b.WriteString(item)
			} else {
				fmt.Fprintf(&b, "• 코드: %s | 만든이: %s", m.ID, m.CreatorName)
			}
			b.WriteString("\n")
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), b.String())
		return
	case "현황", "보드":
		// 세션우선 라우팅: PvP → 레거시 → 없음
		ctx := context.Background()
		roomID := extractRoomID(msg)
		user := userIDFromMessage(msg)

		// 1) PvP 활성 대국 우선 (동일 Room×User)
		if pvpChessMgr != nil {
			if g, err := pvpChessMgr.GetActiveGameByUserInRoom(ctx, user, roomID); err == nil && g != nil {
				obslog.L().Info("route_decision", zap.String("cmd", "status"), zap.String("mode", "pvp"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(user)))
				wDTO, wErr := pvpChessMgr.ToDTOForViewer(ctx, g, g.WhiteID)
				bDTO, bErr := pvpChessMgr.ToDTOForViewer(ctx, g, g.BlackID)
				if wErr != nil || bErr != nil {
					if txt, e := catalog.Render("render.error", nil); e == nil { _ = client.SendMessage(ctx, roomID, txt) } else { _ = client.SendMessage(ctx, roomID, "표시 오류") }
					return
				}
                		rooms := fanoutRooms(ctx, pvpChanMgr, g, roomID)
                		// 현재 방 우선 순위 적용
                		rooms = prioritizeRooms(rooms, roomID)
                		obslog.L().Info("pvp_fanout_targets", zap.Strings("rooms", rooms), zap.String("game_id", g.ID), zap.String("phase", "status"))
                		meta, _, _ := pvpChanMgr.MetaByGame(ctx, g)
                	for i, r := range rooms {
                		viewer := g.WhiteID
                		if meta != nil {
                			if strings.TrimSpace(r) == strings.TrimSpace(meta.CreatorRoom) {
                				viewer = strings.TrimSpace(meta.CreatorID)
                			} else {
                				if strings.TrimSpace(meta.CreatorID) == strings.TrimSpace(g.WhiteID) { viewer = g.BlackID } else { viewer = g.WhiteID }
                			}
                		}
                		vdto := wDTO
                		if strings.TrimSpace(viewer) == strings.TrimSpace(g.BlackID) { vdto = bDTO }
                		if err := presenter.Board(r, "", vdto); err != nil {
                			obslog.L().Warn("pvp_board_send_error",
                				zap.Error(err),
                				zap.String("room_id", r),
                				zap.String("game_id", g.ID),
                				zap.String("phase", "status"),
                			)
                			if txt, e := catalog.Render("board.send.failed", nil); e == nil { _ = client.SendMessage(ctx, r, txt) } else { _ = client.SendMessage(ctx, r, "보드 전송 실패") }
                		}
                		// 방 간 지연 적용(이미지 드롭 완화)
                		if i < len(rooms)-1 {
                			d := time.Duration(cfg.FanoutImageDelayMS) * time.Millisecond
                			if d > 0 { time.Sleep(d) }
                		}
                	}
				return
			}
		}
		// 2) 레거시 활성 세션이 있으면 레거시 현황
		if chess != nil {
			meta := svcchess.SessionMeta{SessionID: sessionIDFor(msg), Room: roomID, Sender: senderName(msg)}
			if st, err := chess.Status(ctx, meta); err == nil && st != nil {
				obslog.L().Info("route_decision", zap.String("cmd", "status"), zap.String("mode", "legacy"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(user)))
				_ = presenter.Board(roomID, formatter.Status(chesspresenterAdaptState(st)), chesspresenterAdaptState(st))
				return
			}
		}
		// 3) 둘 다 없으면 안내
		obslog.L().Info("route_decision", zap.String("cmd", "status"), zap.String("mode", "none"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(user)))
		if txt, err := catalog.Render("no.active.game", nil); err == nil {
			_ = client.SendMessage(ctx, roomID, txt)
		} else {
			_ = client.SendMessage(ctx, roomID, "활성 대국이 없습니다.")
		}
		return
	case "기권":
		// 세션우선 라우팅: PvP → 레거시 → 없음
		ctx := context.Background()
		roomID := extractRoomID(msg)
		user := userIDFromMessage(msg)

		// 1) PvP 활성 대국이 있으면 PvP 기권
                if pvpChessMgr != nil {
                    if gInRoom, _ := pvpChessMgr.GetActiveGameByUserInRoom(ctx, user, roomID); gInRoom != nil {
                        obslog.L().Info("route_decision", zap.String("cmd", "resign"), zap.String("mode", "pvp"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(user)))
                        preID := gInRoom.ID
                        g, _, err := pvpChessMgr.ResignByRoom(ctx, user, roomID)
                        if err != nil || g == nil {
                            // 최종 상태 재조회: 이미 종료되었으면 개인화 안내만 전송
                            if gFinal, _ := pvpChessMgr.LoadGame(ctx, preID); gFinal != nil && gFinal.Status != pvpchess.StatusActive {
                                // 아래 공용 분기와 동일한 개인화 전송 로직 사용
                                resignerName := gFinal.WhiteName
                                if strings.TrimSpace(user) == strings.TrimSpace(gFinal.BlackID) { resignerName = gFinal.BlackName }
                                if strings.TrimSpace(resignerName) == "" { resignerName = strings.TrimSpace(senderName(msg)) }
                                winnerName := ""
                                if strings.TrimSpace(gFinal.Winner) == strings.TrimSpace(gFinal.WhiteID) { winnerName = gFinal.WhiteName }
                                if strings.TrimSpace(gFinal.Winner) == strings.TrimSpace(gFinal.BlackID) { winnerName = gFinal.BlackName }
                                finishText, _ := catalog.Render("pvp.resign.announce", map[string]string{"ResignerName": strings.TrimSpace(resignerName), "WinnerName": strings.TrimSpace(winnerName)})
                                rooms := fanoutRooms(ctx, pvpChanMgr, gFinal, roomID)
                                // 현재 방 우선 순위 적용
                                rooms = prioritizeRooms(rooms, roomID)
                                obslog.L().Info("pvp_fanout_targets", zap.Strings("rooms", rooms), zap.String("game_id", gFinal.ID), zap.String("phase", "resign"))
                                meta, _, _ := pvpChanMgr.MetaByGame(ctx, gFinal)
                                for i, r := range rooms {
                                    viewer := strings.TrimSpace(gFinal.WhiteID)
                                    if meta != nil {
                                        if strings.TrimSpace(r) == strings.TrimSpace(meta.CreatorRoom) {
                                            viewer = strings.TrimSpace(meta.CreatorID)
                                        } else {
                                            if strings.TrimSpace(meta.CreatorID) == strings.TrimSpace(gFinal.WhiteID) { viewer = strings.TrimSpace(gFinal.BlackID) } else { viewer = strings.TrimSpace(gFinal.WhiteID) }
                                        }
                                    }
                                    var msgText string
                                    if viewer == strings.TrimSpace(user) {
                                        if t, e := catalog.Render("pvp.resign.loser", nil); e == nil { msgText = t }
                                    } else {
                                        if t, e := catalog.Render("pvp.resign.winner", nil); e == nil { msgText = t }
                                    }
                                    if strings.TrimSpace(msgText) == "" { msgText = finishText }
                                    if strings.TrimSpace(msgText) == "" {
                                        if tt, ee := catalog.Render("board.send.failed", nil); ee == nil { msgText = tt } else { msgText = "보드 전송 실패" }
                                    }
                                    _ = client.SendMessage(ctx, r, msgText)
                                    // 방 간 지연 적용(드롭 완화)
                                    if i < len(rooms)-1 {
                                        d := time.Duration(cfg.FanoutImageDelayMS) * time.Millisecond
                                        if d > 0 { time.Sleep(d) }
                                    }
                                }
                                return
                            }
                            // 여전히 ACTIVE → 실패 안내
                            if t, e := catalog.Render("resign.process.error", nil); e == nil {
                                _ = client.SendMessage(ctx, roomID, t)
                            } else {
                                _ = client.SendMessage(ctx, roomID, "기권 처리 실패")
                            }
                            return
                        }
                // YAML-only resign announce (이미지 전송 제거: 텍스트만 전송)
                resignerName := g.WhiteName
                if strings.TrimSpace(user) == strings.TrimSpace(g.BlackID) {
                    resignerName = g.BlackName
                }
                if strings.TrimSpace(resignerName) == "" {
                    resignerName = strings.TrimSpace(senderName(msg))
                }
                winnerName := ""
                if strings.TrimSpace(g.Winner) == strings.TrimSpace(g.WhiteID) {
                    winnerName = g.WhiteName
                }
                if strings.TrimSpace(g.Winner) == strings.TrimSpace(g.BlackID) {
                    winnerName = g.BlackName
                }
                finishText, _ := catalog.Render("pvp.resign.announce", map[string]string{
                    "ResignerName": strings.TrimSpace(resignerName),
                    "WinnerName":   strings.TrimSpace(winnerName),
                })
                obslog.L().Info("pvp_resign_announce",
                    zap.String("game_id", g.ID),
                    zap.String("resigner_id", strings.TrimSpace(user)),
                    zap.String("resigner_name", strings.TrimSpace(resignerName)),
                    zap.String("winner_id", strings.TrimSpace(g.Winner)),
                    zap.String("winner_name", strings.TrimSpace(winnerName)),
                )
                rooms := fanoutRooms(ctx, pvpChanMgr, g, roomID)
                // 현재 방 우선 순위 적용
                rooms = prioritizeRooms(rooms, roomID)
                obslog.L().Info("pvp_fanout_targets", zap.Strings("rooms", rooms), zap.String("game_id", g.ID), zap.String("phase", "resign"))
                meta, _, _ := pvpChanMgr.MetaByGame(ctx, g)
                for i, r := range rooms {
                    // 방별 뷰어 판별: 생성자 방은 생성자, 상대 방은 상대 참가자
                    viewer := strings.TrimSpace(g.WhiteID)
                    if meta != nil {
                        if strings.TrimSpace(r) == strings.TrimSpace(meta.CreatorRoom) {
                            viewer = strings.TrimSpace(meta.CreatorID)
                        } else {
                            if strings.TrimSpace(meta.CreatorID) == strings.TrimSpace(g.WhiteID) { viewer = strings.TrimSpace(g.BlackID) } else { viewer = strings.TrimSpace(g.WhiteID) }
                        }
                    }

                    // 개인화 문구: 기권자(패배), 승자(승리)
                    var msgText string
                    if viewer == strings.TrimSpace(user) {
                        if t, e := catalog.Render("pvp.resign.loser", nil); e == nil { msgText = t }
                    } else {
                        if t, e := catalog.Render("pvp.resign.winner", nil); e == nil { msgText = t }
                    }
                    if strings.TrimSpace(msgText) == "" {
                        // 폴백: 공용 안내 또는 고정 문자열
                        msgText = finishText
                        if strings.TrimSpace(msgText) == "" {
                            if tt, ee := catalog.Render("board.send.failed", nil); ee == nil { msgText = tt } else { msgText = "보드 전송 실패" }
                        }
                    }
                    if err := client.SendMessage(ctx, r, msgText); err != nil {
                        obslog.L().Warn("pvp_resign_send_error", zap.Error(err), zap.String("room_id", r), zap.String("game_id", g.ID))
                    }
                    // 방 간 지연 적용(드롭 완화)
                    if i < len(rooms)-1 {
                        d := time.Duration(cfg.FanoutImageDelayMS) * time.Millisecond
                        if d > 0 { time.Sleep(d) }
                    }
                }
                return
			}
		}
		// 2) 레거시 활성 세션이 있으면 레거시 기권
		if chess != nil {
			meta := svcchess.SessionMeta{SessionID: sessionIDFor(msg), Room: roomID, Sender: senderName(msg)}
			if st, err := chess.Status(ctx, meta); err == nil && st != nil {
				obslog.L().Info("route_decision", zap.String("cmd", "resign"), zap.String("mode", "legacy"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(user)))
				state, rerr := chess.Resign(ctx, meta)
				if rerr != nil {
					if txt, e := catalog.Render("resign.failed", map[string]string{"Error": rerr.Error()}); e == nil {
						_ = client.SendMessage(ctx, roomID, txt)
					} else {
						_ = client.SendMessage(ctx, roomID, "기권 실패: "+rerr.Error())
					}
					return
				}
				_ = presenter.Board(roomID, formatter.Resign(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
				return
			}
		}
		obslog.L().Info("route_decision", zap.String("cmd", "resign"), zap.String("mode", "none"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(user)))
		if txt, err := catalog.Render("no.active.game", nil); err == nil {
			_ = client.SendMessage(ctx, roomID, txt)
		} else {
			_ = client.SendMessage(ctx, roomID, "활성 대국이 없습니다.")
		}
		return
	default:
		// 공통 명령(이동): 세션우선 라우팅
		ctx := context.Background()
		roomID := extractRoomID(msg)
		moveInput := strings.TrimSpace(raw)

		// 1) PvP 이동 시도 (없으면 조용히 패스)
    if pvpChessMgr != nil {
            if handlePvPMove(client, cfg, pvpChessMgr, pvpChanMgr, presenter, catalog, msg, moveInput, false) {
                obslog.L().Info("route_decision", zap.String("cmd", "move"), zap.String("mode", "pvp"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(userIDFromMessage(msg))))
                return
            }
    }
		// 2) 레거시 세션이 활성인지 확인 후 이동
		if chess != nil {
			meta := svcchess.SessionMeta{SessionID: sessionIDFor(msg), Room: roomID, Sender: senderName(msg)}
			if st, err := chess.Status(ctx, meta); err == nil && st != nil {
				obslog.L().Info("route_decision", zap.String("cmd", "move"), zap.String("mode", "legacy"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(userIDFromMessage(msg))))
				summary, perr := chess.Play(ctx, meta, moveInput)
				if perr != nil {
					if txt, e := catalog.Render("move.failed_with_error", map[string]string{"Error": perr.Error()}); e == nil {
						_ = client.SendMessage(ctx, roomID, txt)
					} else {
						_ = client.SendMessage(ctx, roomID, "이동 실패: "+perr.Error())
					}
					return
				}
				dto := chesspresenterAdaptSummary(summary)
				_ = presenter.Board(roomID, formatter.Move(dto), dto.State)
				return
			}
		}
		// 3) 둘 다 없으면 안내(세션 없음)
		obslog.L().Info("route_decision", zap.String("cmd", "move"), zap.String("mode", "none"), zap.String("room_id", roomID), zap.String("user", strings.TrimSpace(userIDFromMessage(msg))))
		if txt, err := catalog.Render("no.active.game", nil); err == nil {
			_ = client.SendMessage(ctx, roomID, txt)
		} else {
			_ = client.SendMessage(ctx, roomID, "활성 대국이 없습니다.")
		}
		return
	}
}

func handlePvPMove(client *irisfast.Client, cfg *appcfg.AppConfig, pvpChessMgr *pvpchess.Manager, pvpChanMgr *pvpchan.Manager, presenter *chesspresenter.Presenter, catalog *msgcat.Catalog, msg *irisfast.Message, moveInput string, strict bool) bool {
	if pvpChessMgr == nil || pvpChanMgr == nil {
		return false
	}
	ctx := context.Background()
	roomID := extractRoomID(msg)
	userID := strings.TrimSpace(userIDFromMessage(msg))
	if roomID == "" {
		return strict
	}
	if userID == "" {
		if strict {
			if txt, e := catalog.Render("user.identify.error", nil); e == nil {
				_ = client.SendMessage(ctx, roomID, txt)
			} else {
				_ = client.SendMessage(ctx, roomID, "사용자 식별 실패")
			}
		}
		return strict
	}
	moveInput = strings.TrimSpace(moveInput)
	if moveInput == "" {
		if strict {
			if txt, e := catalog.Render("move.bad_input", nil); e == nil {
				_ = client.SendMessage(ctx, roomID, txt)
			} else {
				_ = client.SendMessage(ctx, roomID, "이동 실패: 잘못된 입력")
			}
		}
		return strict
	}

	gameInRoom, err := pvpChessMgr.GetActiveGameByUserInRoom(ctx, userID, roomID)
	if err != nil {
		obslog.L().Warn("pvp_lookup_error", zap.Error(err), zap.String("user_id", userID), zap.String("room_id", roomID))
		if txt, e := catalog.Render("move.state.error", nil); e == nil {
			_ = client.SendMessage(ctx, roomID, txt)
		} else {
			_ = client.SendMessage(ctx, roomID, "이동 실패: 대국 상태 조회 오류")
		}
		return true
	}
	if gameInRoom == nil {
		if strict {
			if txt, e := catalog.Render("no.active.game", nil); e == nil {
				_ = client.SendMessage(ctx, roomID, txt)
			} else {
				_ = client.SendMessage(ctx, roomID, "활성 대국이 없습니다.")
			}
			return true
		}
		return false
	}

    oldLen := 0
    if gameInRoom != nil { oldLen = len(gameInRoom.MovesUCI) }
    game, resultText, playErr := pvpChessMgr.PlayMoveByRoom(ctx, userID, roomID, moveInput)
    if playErr != nil || game == nil {
        msgText, _ := catalog.Render("move.failed", nil)
        if playErr != nil {
            obslog.L().Warn("pvp_move_error", zap.Error(playErr), zap.String("user_id", userID), zap.String("game_id", gameInRoom.ID))
            if t, e := catalog.Render("move.failed_with_error", map[string]string{"Error": playErr.Error()}); e == nil {
                msgText = t
            } else {
                msgText = "이동 실패: " + playErr.Error()
            }
        }
        _ = client.SendMessage(ctx, roomID, msgText)
        return true
    }

    // 적용 여부 판별: 수가 적용되지 않았다면(차례 아님/불법수/경합) 텍스트만 전송하고 종료
    if len(game.MovesUCI) <= oldLen {
        msgText := strings.TrimSpace(resultText)
        if msgText == "" {
            if t, e := catalog.Render("move.failed", nil); e == nil { msgText = t } else { msgText = "이동 실패" }
        }
        _ = client.SendMessage(ctx, roomID, msgText)
        return true
    }

wDTO, wErr := pvpChessMgr.ToDTOForViewer(ctx, game, game.WhiteID)
bDTO, bErr := pvpChessMgr.ToDTOForViewer(ctx, game, game.BlackID)
if wErr != nil || bErr != nil {
    obslog.L().Warn("pvp_render_error", zap.Error(func() error { if wErr != nil { return wErr }; return bErr }()), zap.String("game_id", game.ID))
    fallback := strings.TrimSpace(resultText)
    if fallback == "" {
        if t, e := catalog.Render("render.board.failed", nil); e == nil { fallback = t } else { fallback = "보드 렌더링 실패" }
    }
    _ = client.SendMessage(ctx, roomID, fallback)
    return true
}

	moveText := ""
	if game.Status == pvpchess.StatusFinished {
		winner := game.WhiteName
		if game.Outcome == "black" {
			winner = game.BlackName
		}
		if t, e := catalog.Render("finish.checkmate", map[string]string{"Winner": winner}); e == nil {
			moveText = t
		} else {
			moveText = "✅ 승리했습니다! 축하드립니다."
		}
	} else if game.Status == pvpchess.StatusDraw {
		if t, e := catalog.Render("finish.draw", nil); e == nil {
			moveText = t
		} else {
			moveText = "🤝 무승부로 종료되었습니다."
		}
	}

    rooms := fanoutRooms(ctx, pvpChanMgr, game, roomID)
    // 현재 방 우선 순위 적용
    rooms = prioritizeRooms(rooms, roomID)
    obslog.L().Info("pvp_fanout_targets", zap.Strings("rooms", rooms), zap.String("game_id", game.ID), zap.String("phase", "move"))
    meta, _, _ := pvpChanMgr.MetaByGame(ctx, game)

fallback := strings.TrimSpace(resultText)
    for i, r := range rooms {
        viewer := game.WhiteID
        if meta != nil {
            if strings.TrimSpace(r) == strings.TrimSpace(meta.CreatorRoom) {
                viewer = strings.TrimSpace(meta.CreatorID)
            } else {
                if strings.TrimSpace(meta.CreatorID) == strings.TrimSpace(game.WhiteID) { viewer = game.BlackID } else { viewer = game.WhiteID }
            }
        }
        vdto := wDTO
        if strings.TrimSpace(viewer) == strings.TrimSpace(game.BlackID) { vdto = bDTO }
        if err := presenter.Board(r, moveText, vdto); err != nil {
            obslog.L().Warn("pvp_board_send_error", zap.Error(err), zap.String("room_id", r), zap.String("game_id", game.ID))
            txt := moveText
            if strings.TrimSpace(txt) == "" {
                txt = fallback
            }
            if strings.TrimSpace(txt) == "" {
                if t, e := catalog.Render("board.send.failed", nil); e == nil {
                    txt = t
                } else {
                    txt = "보드 전송 실패"
                }
            }
            _ = client.SendMessage(ctx, r, txt)
        }
        // 방 간 지연 적용(이미지 드롭 완화)
        if i < len(rooms)-1 {
            d := time.Duration(cfg.FanoutImageDelayMS) * time.Millisecond
            if d > 0 { time.Sleep(d) }
        }
    }
	return true
}

// helpText removed: YAML-only catalog is the single source for help content.

func userIDFromMessage(msg *irisfast.Message) string {
	if msg.JSON != nil && msg.JSON.UserID != "" {
		return msg.JSON.UserID
	}
	if msg.Sender != nil {
		return strings.TrimSpace(*msg.Sender)
	}
	return ""
}

// mergeFanoutRooms는 전달받은 방 목록에 게임의 원/해결 방을 합쳐 중복 제거.
// 이유: 참가자 인덱스가 늦게 동기화되면 한쪽 방만 반환될 수 있음.
func mergeFanoutRooms(g *pvpchess.Game, rooms []string) []string {
	set := make(map[string]struct{})
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		set[s] = struct{}{}
	}
	for _, r := range rooms {
		add(r)
	}
	if g != nil {
		add(g.OriginRoom)
		add(g.ResolveRoom)
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	return out
}

// fanoutRooms: 채널 저장소/사용자 인덱스/현재 방을 합쳐 안정적 팬아웃 대상 생성
func fanoutRooms(ctx context.Context, pvpChanMgr *pvpchan.Manager, g *pvpchess.Game, extras ...string) []string {
    base := extras
    if pvpChanMgr != nil && g != nil {
        if meta, code, _ := pvpChanMgr.MetaByGame(ctx, g); meta != nil || code != "" {
            if rs, _ := pvpChanMgr.Rooms(ctx, code); len(rs) > 0 {
                base = append(base, rs...)
            }
        }
        if rs, _ := pvpChanMgr.RoomsByUserAndGame(ctx, g.WhiteID, g.ID); len(rs) > 0 {
            base = append(base, rs...)
        }
        if rs, _ := pvpChanMgr.RoomsByUserAndGame(ctx, g.BlackID, g.ID); len(rs) > 0 {
            base = append(base, rs...)
        }
    }
    return mergeFanoutRooms(g, base)
}

// prioritizeRooms: 현재 방을 선두로 이동(없으면 추가) — 순회 중 드롭 완화 우선순위 확보
func prioritizeRooms(rooms []string, current string) []string {
    cur := strings.TrimSpace(current)
    if cur == "" {
        return rooms
    }
    seen := make(map[string]struct{})
    out := make([]string, 0, len(rooms)+1)
    // 현재 방 먼저
    out = append(out, cur)
    seen[cur] = struct{}{}
    // 나머지 보존 + 중복 제거
    for _, r := range rooms {
        rr := strings.TrimSpace(r)
        if rr == "" { continue }
        if _, ok := seen[rr]; ok { continue }
        out = append(out, rr)
        seen[rr] = struct{}{}
    }
    return out
}

func roomAllowed(allowed []string, msg *irisfast.Message) bool {
	if msg == nil || msg.JSON == nil {
		return false
	}
	rid := extractRoomID(msg)
	if rid == "" {
		return false
	}
	if _, err := strconv.ParseInt(rid, 10, 64); err != nil {
		return false
	}
	for _, r := range allowed {
		if r == rid {
			return true
		}
	}
	return false
}

func extractRoomID(msg *irisfast.Message) string {
	if msg == nil {
		return ""
	}
	// Prefer JSON RoomID if provided
	if msg.JSON != nil {
		rid := sanitizeRoomID(msg.JSON.RoomID)
		if rid != "" {
			return rid
		}
		// Fallback to ChatID used by legacy Iris schema
		cid := sanitizeRoomID(msg.JSON.ChatID)
		if cid != "" {
			return cid
		}
	}
	// Fallback to Room field: try numeric digits first
	sr := sanitizeRoomID(msg.Room)
	if sr != "" {
		return sr
	}
	// As a last resort, use sanitized room name (text). Some Iris deployments route by name.
	rname := sanitizeText(msg.Room)
	if rname != "" {
		return rname
	}
	return ""
}

func handleChessCommand(client *irisfast.Client, cfg *appcfg.AppConfig, chess *svcchess.Service, presenter *chesspresenter.Presenter, formatter *chesspresenter.Formatter, catalog *msgcat.Catalog, msg *irisfast.Message, args []string) {
	meta := svcchess.SessionMeta{
		SessionID: sessionIDFor(msg),
		Room:      extractRoomID(msg),
		Sender:    senderName(msg),
	}
	if len(args) == 0 {
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Help())
		return
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	ctx := context.Background()

	switch sub {
	case "시작":
		preset := ""
		if len(args) >= 2 {
			preset = args[1]
		}
		state, err := chess.StartSession(ctx, meta, preset, false)
		resumed := false
		if err != nil {
			if errorsEqual(err, svcchess.ErrSessionInProgress) {
				st, sErr := chess.Status(ctx, meta)
				if sErr == nil {
					state = st
					resumed = true
					err = nil
				}
			}
		}
		if err != nil {
			if txt, e := catalog.Render("chess.start.failed", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "체스 시작 실패: "+err.Error())
			}
			return
		}
		_ = presenter.Board(extractRoomID(msg), formatter.Start(chesspresenterAdaptState(state), resumed), chesspresenterAdaptState(state))
	case "현황":
		state, err := chess.Status(ctx, meta)
		if err != nil {
			if txt, e := catalog.Render("chess.status.error", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "체스 현황 오류: "+err.Error())
			}
			return
		}
		_ = presenter.Board(extractRoomID(msg), formatter.Status(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
	case "무르기":
		state, err := chess.Undo(ctx, meta)
		if err != nil {
			if txt, e := catalog.Render("chess.undo.failed", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "무르기 실패: "+err.Error())
			}
			return
		}
		_ = presenter.Board(extractRoomID(msg), formatter.Undo(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
	case "기권":
		state, err := chess.Resign(ctx, meta)
		if err != nil {
			if txt, e := catalog.Render("resign.failed", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "기권 실패: "+err.Error())
			}
			return
		}
		_ = presenter.Board(extractRoomID(msg), formatter.Resign(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
	case "기록":
		limit := 10
		if len(args) >= 2 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 {
				limit = n
			}
		}
		games, err := chess.History(ctx, meta, limit)
		if err != nil {
			if txt, e := catalog.Render("chess.history.error", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "기록 조회 실패: "+err.Error())
			}
			return
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.History(chesspresenter.ToDTOGames(games)))
	case "기보":
		if len(args) < 2 {
			if txt, e := catalog.Render("usage.game", map[string]string{"Prefix": cfg.BotPrefix}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "용법: "+cfg.BotPrefix+" 기보 <ID>")
			}
			return
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			if txt, e := catalog.Render("game.id.invalid", nil); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "잘못된 ID")
			}
			return
		}
		game, err := chess.Game(ctx, meta, id)
		if err != nil {
			if txt, e := catalog.Render("game.fetch.error", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "기보 조회 실패: "+err.Error())
			}
			return
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Game(chesspresenter.ToDTOGame(game)))
	case "프로필":
		profile, err := chess.Profile(ctx, meta)
		if err != nil {
			if txt, e := catalog.Render("chess.profile.error", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "프로필 조회 실패: "+err.Error())
			}
			return
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Profile(chesspresenterAdaptProfile(profile)))
	case "선호":
		if len(args) < 2 {
			if txt, e := catalog.Render("usage.preset", map[string]string{"Prefix": cfg.BotPrefix}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "용법: "+cfg.BotPrefix+" 선호 <preset>")
			}
			return
		}
		profile, err := chess.UpdatePreferredPreset(ctx, meta, args[1])
		if err != nil {
			if txt, e := catalog.Render("chess.preset.update.failed", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "선호 난이도 업데이트 실패: "+err.Error())
			}
			return
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.PreferredPresetUpdated(chesspresenterAdaptProfile(profile)))
	case "도움":
		suggestion, err := chess.Assist(ctx, meta)
		if err != nil {
			if txt, e := catalog.Render("chess.assist.failed", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "추천 수 계산 실패: "+err.Error())
			}
			return
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Assist(chesspresenterAdaptAssist(suggestion)))
	default:
		summary, err := chess.Play(ctx, meta, sub)
		if err != nil {
			if txt, e := catalog.Render("move.failed_with_error", map[string]string{"Error": err.Error()}); e == nil {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), txt)
			} else {
				_ = client.SendMessage(context.Background(), extractRoomID(msg), "이동 실패: "+err.Error())
			}
			return
		}
		dto := chesspresenterAdaptSummary(summary)
		text := formatter.Move(dto)
		_ = presenter.Board(extractRoomID(msg), text, dto.State)
	}
}

func chesspresenterAdaptState(s *svcchess.SessionState) *chessdto.SessionState {
	return chesspresenter.ToDTOState(s)
}
func chesspresenterAdaptSummary(m *svcchess.MoveSummary) *chessdto.MoveSummary {
	return chesspresenter.ToDTOMoveSummary(m)
}
func chesspresenterAdaptProfile(p *domain.ChessProfile) *chessdto.ChessProfile {
	return chesspresenter.ToDTOProfile(p)
}
func chesspresenterAdaptAssist(a *svcchess.AssistSuggestion) *chessdto.AssistSuggestion {
	return chesspresenter.ToDTOAssist(a)
}

func sessionIDFor(msg *irisfast.Message) string {
	uid := userIDFromMessage(msg)
	if uid == "" {
		uid = senderName(msg)
	}
	return fmt.Sprintf("%s:%s", strings.TrimSpace(extractRoomID(msg)), strings.TrimSpace(uid))
}

func senderName(msg *irisfast.Message) string {
	if msg.Sender != nil && strings.TrimSpace(*msg.Sender) != "" {
		return strings.TrimSpace(*msg.Sender)
	}
	if msg.JSON != nil && strings.TrimSpace(msg.JSON.UserID) != "" {
		return strings.TrimSpace(msg.JSON.UserID)
	}
	return "player"
}

type prefixProvider struct{ prefix string }

func (p prefixProvider) Prefix() string { return p.prefix }

func errorsEqual(err error, target error) bool {
	return err != nil && target != nil && err.Error() == target.Error()
}

// legacyFinishText removed: now replaced by YAML templates finish.*

// messageText returns the canonical text content from a WS message.
// Prefer Msg; fallback to JSON.Message when Msg is empty.
func messageText(msg *irisfast.Message) string {
	if msg == nil {
		return ""
	}
	if strings.TrimSpace(msg.Msg) != "" {
		return strings.TrimSpace(msg.Msg)
	}
	if msg.JSON != nil && strings.TrimSpace(msg.JSON.Message) != "" {
		return strings.TrimSpace(msg.JSON.Message)
	}
	return ""
}

// sanitizeText removes zero-width and non-breaking whitespace that may appear in Kakao frames.
func sanitizeText(s string) string {
	if s == "" {
		return s
	}
	// remove common zero-width characters and NBSP
	replacers := []string{
		"\u200b", "", // ZERO WIDTH SPACE
		"\u200c", "", // ZERO WIDTH NON-JOINER
		"\u200d", "", // ZERO WIDTH JOINER
		"\u2060", "", // WORD JOINER
		"\ufeff", "", // ZERO WIDTH NO-BREAK SPACE (BOM)
		"\u00a0", " ", // NO-BREAK SPACE → regular space
	}
	r := strings.NewReplacer(replacers...)
	out := r.Replace(s)
	return strings.TrimSpace(out)
}

// sanitizeRoomID extracts digits only to robustly handle stray control/zero-width characters.
func sanitizeRoomID(s string) string {
	if s == "" {
		return ""
	}
	// First remove zero-widths and NBSP to prevent oddities
	cleaned := sanitizeText(s)
	var b strings.Builder
	for _, r := range cleaned {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isIgnoredSender(cfg *appcfg.AppConfig, name string) bool {
	if cfg == nil {
		return false
	}
	s := strings.TrimSpace(name)
	if s == "" {
		return false
	}
	for _, ig := range cfg.IgnoreSenders {
		if strings.EqualFold(strings.TrimSpace(ig), s) {
			return true
		}
	}
	return false
}
