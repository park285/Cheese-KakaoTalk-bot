package main

import (
    "context"
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
    "github.com/park285/Cheese-KakaoTalk-bot/internal/obslog"
    "github.com/park285/Cheese-KakaoTalk-bot/internal/pvpchan"
    "github.com/park285/Cheese-KakaoTalk-bot/internal/pvpchess"
	svcchess "github.com/park285/Cheese-KakaoTalk-bot/internal/service/chess"
	"github.com/park285/Cheese-KakaoTalk-bot/pkg/chessdto"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func main() {
    // 로깅 초기화(콘솔+파일)
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



    // sender merge 캐시: 최소 프레임(sender만) → 직후 JSON 프레임과 합성 표시용
    type cachedSender struct{ name string; ts time.Time }
    senderCache := make(map[string]cachedSender)
    var senderMu sync.Mutex
    senderTTL := 2 * time.Second

    // Command handler
    ws.OnMessage(func(msg *irisfast.Message) {
        if msg == nil || msg.Msg == "" {
            return
        }
        rid := extractRoomID(msg)
        trimmed := strings.TrimSpace(msg.Msg)

        // 최소 프레임(sender만, JSON 없음) → 캐시만 기록하고 종료
        if msg.JSON == nil || strings.TrimSpace(msg.JSON.ChatID) == "" {
            if rid == "" || trimmed == "" { return }
            if msg.Sender != nil {
                name := strings.TrimSpace(*msg.Sender)
                if name != "" {
                    key := rid + "|" + trimmed
                    now := time.Now()
                    senderMu.Lock()
                    // prune 오래된 항목 정리
                    for k, v := range senderCache {
                        if now.Sub(v.ts) > senderTTL*2 {
                            delete(senderCache, k)
                        }
                    }
                    senderCache[key] = cachedSender{name: name, ts: now}
                    senderMu.Unlock()
                }
            }
            return
        }

        // JSON 프레임: 캐시 합성으로 표시명 우선
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

        // 수신 진단(임시): room / room_id / user 표시
        logger.Info("recv_message", zap.String("room", msg.Room), zap.String("room_id", rid), zap.String("user", strings.TrimSpace(displayUser)))
        // 방 필터: room_id(숫자)만 허용
	        if len(cfg.AllowedRooms) > 0 && !roomAllowed(cfg.AllowedRooms, msg) {
	            logger.Debug("drop_message", zap.String("reason", "room_not_allowed"), zap.String("room_id", extractRoomID(msg)))
	            return
	        }
        // prefix check
        trimmed = strings.TrimSpace(msg.Msg)
        if !strings.HasPrefix(trimmed, cfg.BotPrefix) {
            return
        }
		// Avoid blocking the WS loop
		var svc *svcchess.Service
		if deps != nil {
			svc = deps.Service
		}
    go handleCommand(client, cfg, pvpChessMgr, pvpChanMgr, svc, presenter, formatter, msg)
    })

	// Connect WS
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := ws.Connect(cctx); err != nil {
			cancel()
			logger.Fatal("ws_connect_error", zap.Error(err))
		}
		cancel()

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	_ = ws.Close(context.Background())
	_ = pvpChessMgr.Close()
	_ = pvpRepo.Close()
}

func handleCommand(client *irisfast.Client, cfg *appcfg.AppConfig, pvpChessMgr *pvpchess.Manager, pvpChanMgr *pvpchan.Manager, chess *svcchess.Service, presenter *chesspresenter.Presenter, formatter *chesspresenter.Formatter, msg *irisfast.Message) {
	// strip prefix
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg.Msg), cfg.BotPrefix))
    if raw == "" {
		_ = client.SendMessage(context.Background(), extractRoomID(msg), helpText(cfg))
		return
	}
	// split cmd
	parts := strings.Fields(raw)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "help", "도움":
		_ = client.SendMessage(context.Background(), extractRoomID(msg), helpText(cfg))
	case "방":
		// 방 리스트: 대기 중인 채널 목록을 코드와 함께 표시
		if len(args) >= 1 {
			sub := strings.ToLower(strings.TrimSpace(args[0]))
			if sub == "리스트" || sub == "목록" || sub == "list" {
				metas, err := pvpChanMgr.ListLobby(context.Background())
				if err != nil {
					_ = client.SendMessage(context.Background(), extractRoomID(msg), "방 목록 조회 실패")
					return
				}
				if len(metas) == 0 {
					_ = client.SendMessage(context.Background(), extractRoomID(msg), "대기 중인 방이 없습니다.")
					return
				}
				var b strings.Builder
				b.WriteString("대기 중인 방:\n")
				for _, m := range metas {
					fmt.Fprintf(&b, "• 코드: %s | 만든이: %s\n", m.ID, m.CreatorName)
				}
				_ = client.SendMessage(context.Background(), extractRoomID(msg), b.String())
				return
			} else if sub == "생성" || sub == "만들기" || sub == "create" {
				user := userIDFromMessage(msg)
				if user == "" {
					_ = client.SendMessage(context.Background(), extractRoomID(msg), "사용자 식별 실패")
					return
				}
                mr, err := pvpChanMgr.Make(context.Background(), extractRoomID(msg), user, senderName(msg), pvpchan.ColorRandom)
				if err != nil {
					_ = client.SendMessage(context.Background(), extractRoomID(msg), "채널 생성 실패: "+err.Error())
					return
				}
				_ = client.SendMessage(context.Background(), extractRoomID(msg), fmt.Sprintf("채널 코드: %s\n상대는 '%s 참가 %s'로 참가하세요.", mr.Code, cfg.BotPrefix, mr.Code))
				return
			}
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), "용법: "+cfg.BotPrefix+" 방 생성 | "+cfg.BotPrefix+" 방 리스트")
		return
	case "참가", "방참가":
		if len(args) < 1 {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "용법: "+cfg.BotPrefix+" 참가 <코드>")
			return
		}
		code := strings.TrimSpace(args[0])
		user := userIDFromMessage(msg)
		jr, err := pvpChanMgr.Join(context.Background(), extractRoomID(msg), code, user, senderName(msg), pvpchan.ColorRandom)
		if err != nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "참가 실패: "+err.Error())
			return
		}
		if !jr.Started {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "참가 완료. 상대를 기다리는 중…")
			return
		}
		g, _ := pvpChessMgr.GetActiveGameByUser(context.Background(), user)
		if g == nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "대국 정보를 찾을 수 없습니다.")
			return
		}
		dto, derr := pvpChessMgr.ToDTO(context.Background(), g)
		if derr != nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "표시 오류")
			return
		}
		rooms, _ := pvpChanMgr.Rooms(context.Background(), code)
		text := fmt.Sprintf("♟️ 대국 시작 — %s vs %s", g.WhiteName, g.BlackName)
		for _, r := range rooms {
			_ = presenter.Board(r, text, dto)
		}
		return
	case "방생성":
		user := userIDFromMessage(msg)
		if user == "" {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "사용자 식별 실패")
			return
		}
		mr, err := pvpChanMgr.Make(context.Background(), extractRoomID(msg), user, senderName(msg), pvpchan.ColorRandom)
		if err != nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "채널 생성 실패: "+err.Error())
			return
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), fmt.Sprintf("채널 코드: %s\n상대는 '%s 참가 %s'로 참가하세요.", mr.Code, cfg.BotPrefix, mr.Code))
		return
	case "방리스트", "방목록":
		metas, err := pvpChanMgr.ListLobby(context.Background())
		if err != nil {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "방 목록 조회 실패")
			return
		}
		if len(metas) == 0 {
			_ = client.SendMessage(context.Background(), extractRoomID(msg), "대기 중인 방이 없습니다.")
			return
		}
		var b strings.Builder
		b.WriteString("대기 중인 방:\n")
		for _, m := range metas {
			fmt.Fprintf(&b, "• 코드: %s | 만든이: %s\n", m.ID, m.CreatorName)
		}
		_ = client.SendMessage(context.Background(), extractRoomID(msg), b.String())
		return
	case "현황", "보드":
        if !cfg.PvpOnly {
            // fall back to single-player
            handleChessCommand(client, cfg, chess, presenter, formatter, msg, []string{"현황"})
            return
        }
        user := userIDFromMessage(msg)
        g, err := pvpChessMgr.GetActiveGameByUser(context.Background(), user)
        if err != nil || g == nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), "활성 PvP 대국이 없습니다."); return }
        dto, derr := pvpChessMgr.ToDTO(context.Background(), g)
        if derr != nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), "표시 오류"); return }
        rooms, _ := pvpChanMgr.RoomsByUserAndGame(context.Background(), user, g.ID)
        if len(rooms) == 0 { rooms = []string{g.OriginRoom}; if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { rooms = append(rooms, g.ResolveRoom) } }
        for _, r := range rooms { _ = presenter.Board(r, "", dto) }
        return
    // 무승부(제안/수락) 명령 제거: 규칙상 자동 무승부만 허용
    case "기권":
        if !cfg.PvpOnly {
            handleChessCommand(client, cfg, chess, presenter, formatter, msg, []string{"기권"})
            return
        }
        user := userIDFromMessage(msg)
        g, _, err := pvpChessMgr.Resign(context.Background(), user)
        if err != nil || g == nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), "기권 처리 실패"); return }
        dto, _ := pvpChessMgr.ToDTO(context.Background(), g)
        winner := g.WhiteName
        if g.Winner == g.BlackID { winner = g.BlackName }
        finishText := legacyFinishText("resign", winner)
        rooms, _ := pvpChanMgr.RoomsByUserAndGame(context.Background(), user, g.ID)
        if len(rooms) == 0 { rooms = []string{g.OriginRoom}; if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { rooms = append(rooms, g.ResolveRoom) } }
        if dto != nil { for _, r := range rooms { _ = presenter.Board(r, finishText, dto) } } else { for _, r := range rooms { _ = client.SendMessage(context.Background(), r, finishText) } }
        return
    // 중단 기능 제거: top-level 별칭도 제거됨
    default:
        if cfg.PvpOnly {
            user := userIDFromMessage(msg)
            sub := cmd
            g, _, err := pvpChessMgr.PlayMove(context.Background(), user, sub)
            if err != nil || g == nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), "이동 실패"); return }
            dto, derr := pvpChessMgr.ToDTO(context.Background(), g)
            if derr != nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), "표시 오류"); return }
            moveText := ""
            if g.Status == pvpchess.StatusFinished {
                winner := g.WhiteName
                if g.Outcome == "black" { winner = g.BlackName }
                moveText = legacyFinishText("checkmate", winner)
            } else if g.Status == pvpchess.StatusDraw {
                moveText = legacyFinishText("draw", "")
            }
            rooms, _ := pvpChanMgr.RoomsByUserAndGame(context.Background(), user, g.ID)
            if len(rooms) == 0 { rooms = []string{g.OriginRoom}; if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { rooms = append(rooms, g.ResolveRoom) } }
            for _, r := range rooms { _ = presenter.Board(r, moveText, dto) }
            return
        }
        if chess == nil { _ = client.SendMessage(context.Background(), extractRoomID(msg), "싱글 체스 비활성화됨"); return }
        handleChessCommand(client, cfg, chess, presenter, formatter, msg, append([]string{cmd}, args...))
	}
}

func helpText(cfg *appcfg.AppConfig) string {
	p := strings.TrimSpace(cfg.BotPrefix)
		lines := []string{
			"♞ Kakao Chess Bot",
			"",
			"• " + p + " 방 생성",
			"  PvP 채널 생성 및 코드 발급",
			"• " + p + " 방 리스트",
			"  대기 중인 PvP 방 목록(초대 코드 확인)",
			"• " + p + " 참가 <코드>",
			"  코드로 PvP 방 참가",
	        "• " + p + " 보드 | 현황 | <수> | 기권",
	        "  색 배정: 항상 랜덤",
	        "  별칭: 방생성/방리스트/방참가",
		}
	if !cfg.PvpOnly {
		lines = append(lines,
			"• "+p+" 시작 [level1~level8]",
			"  싱글 체스 시작 / 명령: <수>, 무르기, 기권, 현황, 기록, 기보, 프로필",
		)
	} else {
		lines = append(lines,
			"(PvP 전용 모드: 싱글 체스 비활성화)",
		)
	}
	return strings.Join(lines, "\n")
}

func userIDFromMessage(msg *irisfast.Message) string {
	if msg.JSON != nil && msg.JSON.UserID != "" {
		return msg.JSON.UserID
	}
	if msg.Sender != nil {
		return strings.TrimSpace(*msg.Sender)
	}
	return ""
}

// pvp 명령은 제거됨(레거시 한국어 상위 명령으로 통일)

// 방 허용 여부(room_id 숫자 일치)
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

// room_id 추출(비어있으면 "")
func extractRoomID(msg *irisfast.Message) string {
    if msg == nil {
        return ""
    }
    // 1) json.room_id 우선
    if msg.JSON != nil {
        rid := strings.TrimSpace(msg.JSON.RoomID)
        if rid != "" {
            return rid
        }
        // 1b) json.chat_id 폴백 (레거시 호환)
        cid := strings.TrimSpace(msg.JSON.ChatID)
        if cid != "" {
            if _, err := strconv.ParseInt(cid, 10, 64); err == nil {
                return cid
            }
        }
    }
    // 2) top-level room이 숫자면 room_id로 사용
    r := strings.TrimSpace(msg.Room)
    if r != "" {
        if _, err := strconv.ParseInt(r, 10, 64); err == nil {
            return r
        }
    }
    return ""
}

// Chess command handler
func handleChessCommand(client *irisfast.Client, cfg *appcfg.AppConfig, chess *svcchess.Service, presenter *chesspresenter.Presenter, formatter *chesspresenter.Formatter, msg *irisfast.Message, args []string) {
    meta := svcchess.SessionMeta{
        SessionID: sessionIDFor(msg),
        Room:      extractRoomID(msg),
        Sender:    senderName(msg),
    }
	if len(args) == 0 { // help
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
				// status again to get image/state
				st, sErr := chess.Status(ctx, meta)
				if sErr == nil {
					state = st
					resumed = true
					err = nil
				}
			}
		}
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "체스 시작 실패: "+err.Error())
			return
		}
        _ = presenter.Board(extractRoomID(msg), formatter.Start(chesspresenterAdaptState(state), resumed), chesspresenterAdaptState(state))
	case "현황":
		state, err := chess.Status(ctx, meta)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "체스 현황 오류: "+err.Error())
			return
		}
        _ = presenter.Board(extractRoomID(msg), formatter.Status(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
	case "무르기":
		state, err := chess.Undo(ctx, meta)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "무르기 실패: "+err.Error())
			return
		}
        _ = presenter.Board(extractRoomID(msg), formatter.Undo(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
	case "기권":
		state, err := chess.Resign(ctx, meta)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "기권 실패: "+err.Error())
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
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "기록 조회 실패: "+err.Error())
			return
		}
        _ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.History(chesspresenter.ToDTOGames(games)))
	case "기보":
		if len(args) < 2 {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "용법: "+cfg.BotPrefix+" 기보 <ID>")
			return
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "잘못된 ID")
			return
		}
		game, err := chess.Game(ctx, meta, id)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "기보 조회 실패: "+err.Error())
			return
		}
        _ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Game(chesspresenter.ToDTOGame(game)))
	case "프로필":
		profile, err := chess.Profile(ctx, meta)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "프로필 조회 실패: "+err.Error())
			return
		}
        _ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Profile(chesspresenterAdaptProfile(profile)))
	case "선호":
		if len(args) < 2 {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "용법: "+cfg.BotPrefix+" 선호 <preset>")
			return
		}
		profile, err := chess.UpdatePreferredPreset(ctx, meta, args[1])
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "선호 난이도 업데이트 실패: "+err.Error())
			return
		}
        _ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.PreferredPresetUpdated(chesspresenterAdaptProfile(profile)))
	case "도움":
		suggestion, err := chess.Assist(ctx, meta)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "추천 수 계산 실패: "+err.Error())
			return
		}
        _ = client.SendMessage(context.Background(), extractRoomID(msg), formatter.Assist(chesspresenterAdaptAssist(suggestion)))
	default:
		// Treat as a move
		summary, err := chess.Play(ctx, meta, sub)
		if err != nil {
            _ = client.SendMessage(context.Background(), extractRoomID(msg), "이동 실패: "+err.Error())
			return
		}
		dto := chesspresenterAdaptSummary(summary)
		text := formatter.Move(dto)
		// Always draw board even if not finished
        _ = presenter.Board(extractRoomID(msg), text, dto.State)
	}
}

// Helpers/adapters (avoid import bleed in main)
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

// simple error compare without direct import cycles
func errorsEqual(err error, target error) bool {
	return err != nil && target != nil && err.Error() == target.Error()
}

// legacyFinishText returns legacy-style summary text for PvP endings.
// event: "checkmate" | "resign" | "draw"
func legacyFinishText(event string, winner string) string {
	switch strings.ToLower(strings.TrimSpace(event)) {
	case "checkmate":
		if strings.TrimSpace(winner) == "" {
			return "✅ 승리했습니다! 축하드립니다."
		}
		return fmt.Sprintf("✅ 승리했습니다! 축하드립니다. (승자: %s)", winner)
	case "resign":
		if strings.TrimSpace(winner) == "" {
			return "🛑 기권하여 패배로 기록되었습니다."
		}
		return fmt.Sprintf("🛑 기권하여 패배로 기록되었습니다. (승자: %s)", winner)
	case "draw":
		return "🤝 무승부로 종료되었습니다."
	default:
		return ""
	}
}
