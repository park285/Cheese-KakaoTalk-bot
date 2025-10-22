package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "syscall"
    "time"

    appcfg "github.com/kapu/kakao-cheese-bot-go/internal/config"
    "github.com/kapu/kakao-cheese-bot-go/internal/irisfast"
    "github.com/kapu/kakao-cheese-bot-go/internal/pvp"
    "github.com/kapu/kakao-cheese-bot-go/internal/pvpchess"
    "github.com/kapu/kakao-cheese-bot-go/internal/chessbuilder"
    svcchess "github.com/kapu/kakao-cheese-bot-go/internal/service/chess"
    "github.com/kapu/kakao-cheese-bot-go/internal/adapter/chesspresenter"
    "github.com/kapu/kakao-cheese-bot-go/pkg/chessdto"
    "github.com/kapu/kakao-cheese-bot-go/internal/domain"
    "go.uber.org/zap"
)

func main() {
    cfg, err := appcfg.Load()
    if err != nil {
        log.Fatalf("config error: %v", err)
    }

    headers := func() map[string]string {
        h := map[string]string{}
        if cfg.XUserID != "" {
            h["X-User-Id"] = cfg.XUserID
        }
        if cfg.XUserEmail != "" {
            h["X-User-Email"] = cfg.XUserEmail
        }
        if cfg.XSessionID != "" {
            h["X-Session-Id"] = cfg.XSessionID
        }
        return h
    }

    client := irisfast.NewClient(cfg.IrisBaseURL, irisfast.WithHeaderProvider(headers))

    ws := irisfast.NewWebSocket(cfg.IrisWSURL, 5, time.Second)
    // Inject WS handshake headers if required by the server
    ws.SetHeaderProvider(headers)
    ws.OnStateChange(func(state irisfast.WebSocketState) {
        log.Printf("WS state: %s", state)
    })

    pvpMgr := pvp.NewManager()

    // PvP chess manager (Redis-backed)
    pvpChessMgr, err := pvpchess.NewManager(cfg.RedisURL)
    if err != nil {
        log.Fatalf("pvp manager init error: %v", err)
    }
    // PvP DB repository
    pvpRepo, err := pvpchess.NewRepository(cfg.DatabaseURL)
    if err != nil {
        log.Fatalf("pvp repo init error: %v", err)
    }
    pvpChessMgr.AttachRepository(pvpRepo)

    // Chess deps
    deps, err := chessbuilder.New(cfg, zap.NewNop())
    if err != nil {
        log.Fatalf("chess init error: %v", err)
    }
    presenter := chesspresenter.NewPresenter(
        func(room, message string) error { return client.SendMessage(context.Background(), room, message) },
        func(room, imageBase64 string) error { return client.SendImage(context.Background(), room, imageBase64) },
    )
    formatter := chesspresenter.NewFormatter(prefixProvider{prefix: cfg.BotPrefix})

    // Command handler
    ws.OnMessage(func(msg *irisfast.Message) {
        if msg == nil || msg.Msg == "" {
            return
        }
        // room filter: if AllowedRooms configured and msg.Room not in list → ignore
        if len(cfg.AllowedRooms) > 0 && !roomAllowed(cfg.AllowedRooms, msg.Room) {
            log.Printf("ignore message from room=%s (not allowed)", msg.Room)
            return
        }
        // prefix check
        if !strings.HasPrefix(strings.TrimSpace(msg.Msg), cfg.BotPrefix) {
            return
        }
        // Avoid blocking the WS loop
        go handleCommand(client, cfg, pvpMgr, pvpChessMgr, deps.Service, presenter, formatter, msg)
    })

    // Connect WS
    cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    if err := ws.Connect(cctx); err != nil {
        cancel()
        log.Fatalf("ws connect error: %v", err)
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

func handleCommand(client *irisfast.Client, cfg *appcfg.AppConfig, pvpMgr *pvp.Manager, pvpChessMgr *pvpchess.Manager, chess *svcchess.Service, presenter *chesspresenter.Presenter, formatter *chesspresenter.Formatter, msg *irisfast.Message) {
    // strip prefix
    raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg.Msg), cfg.BotPrefix))
    if raw == "" {
        _ = client.SendMessage(context.Background(), msg.Room, helpText(cfg))
        return
    }
    // split cmd
    parts := strings.Fields(raw)
    cmd := strings.ToLower(parts[0])
    args := parts[1:]

    switch cmd {
    case "help":
        _ = client.SendMessage(context.Background(), msg.Room, helpText(cfg))
    case "pvp":
        handlePvpCommand(client, cfg, pvpMgr, pvpChessMgr, presenter, msg, args)
    case "체스":
        handleChessCommand(client, cfg, chess, presenter, formatter, msg, args)
    default:
        _ = client.SendMessage(context.Background(), msg.Room, "Unknown command. Try 'help'.")
    }
}

func helpText(cfg *appcfg.AppConfig) string {
    p := cfg.BotPrefix
    return strings.Join([]string{
        "♞ Kakao Chess Bot",
        "",
        "• " + p + "pvp @상대 [white|black|random] [time 3+2]",
        "  PvP 대국 생성 (자동 수락)",
        "• " + p + "체스 시작 [level1~level8]",
        "  싱글 체스 시작 / 명령: <수>, 무르기, 기권, 현황, 기록, 기보, 프로필",
    }, "\n")
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

func handlePvpCommand(client *irisfast.Client, cfg *appcfg.AppConfig, pvpMgr *pvp.Manager, pvpChessMgr *pvpchess.Manager, presenter *chesspresenter.Presenter, msg *irisfast.Message, args []string) {
    if len(args) < 1 {
        _ = client.SendMessage(context.Background(), msg.Room, "Usage: "+cfg.BotPrefix+" pvp @user [white|black|random] [time 3+2] | pvp 현황 | pvp 기권 | pvp <수>")
        return
    }

    // Branch: creation when first token looks like a user reference
    if strings.HasPrefix(args[0], "@") {
        challenger := userIDFromMessage(msg)
        if challenger == "" {
            _ = client.SendMessage(context.Background(), msg.Room, "Cannot identify challenger user.")
            return
        }
        target := sanitizeUserArg(args[0])
        if target == "" {
            _ = client.SendMessage(context.Background(), msg.Room, "Invalid target user.")
            return
        }

        color := pvp.ColorRandom
        timeCtl := cfg.TimeControl
        if len(args) >= 2 {
            v := strings.ToLower(args[1])
            switch v {
            case "white", "black", "random", "w", "b":
                color = pvp.ParseColorChoice(v)
                if len(args) >= 3 {
                    if strings.ToLower(args[2]) == "time" && len(args) >= 4 {
                        timeCtl = args[3]
                    } else {
                        timeCtl = args[2]
                    }
                }
            case "time":
                if len(args) >= 3 { timeCtl = args[2] }
            default:
                timeCtl = args[1]
            }
        }

        ch, err := pvpMgr.CreateChallenge(msg.Room, challenger, target, color, timeCtl)
        if err != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "PvP error: "+err.Error())
            return
        }
        // Create PvP chess game (auto-accepted)
        g, gerr := pvpChessMgr.CreateGameFromChallenge(context.Background(), ch.OriginRoom, ch.ResolveRoom, ch.ChallengerID, senderName(msg), ch.TargetID, target, string(ch.Color), ch.TimeControl)
        if gerr != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "PvP game error: "+gerr.Error())
            return
        }
        dto, derr := pvpChessMgr.ToDTO(context.Background(), g)
        if derr != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "PvP render error: "+derr.Error())
            return
        }
        text := fmt.Sprintf("♟️ 대국 시작 — %s vs %s (시간 %s)", g.WhiteName, g.BlackName, g.TimeControl)
        _ = presenter.Board(ch.OriginRoom, text, dto)
        if ch.ResolveRoom != "" && ch.ResolveRoom != ch.OriginRoom {
            _ = presenter.Board(ch.ResolveRoom, text, dto)
        }
        return
    }

    // Else: in-game commands for participants
    sub := strings.ToLower(strings.TrimSpace(args[0]))
    user := userIDFromMessage(msg)
    switch sub {
    case "현황", "status":
        g, err := pvpChessMgr.GetActiveGameByUser(context.Background(), user)
        if err != nil || g == nil { _ = client.SendMessage(context.Background(), msg.Room, "활성 PvP 대국이 없습니다."); return }
        dto, derr := pvpChessMgr.ToDTO(context.Background(), g)
        if derr != nil { _ = client.SendMessage(context.Background(), msg.Room, "표시 오류"); return }
        _ = presenter.Board(g.OriginRoom, "", dto)
        if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { _ = presenter.Board(g.ResolveRoom, "", dto) }
        return
    case "기권", "resign":
        g, _, err := pvpChessMgr.Resign(context.Background(), user)
        if err != nil || g == nil { _ = client.SendMessage(context.Background(), msg.Room, "기권 처리 실패"); return }
        dto, _ := pvpChessMgr.ToDTO(context.Background(), g)
        // Compose legacy-style finish text
        winner := g.WhiteName
        if g.Winner == g.BlackID { winner = g.BlackName }
        finishText := legacyFinishText("resign", winner)
        if dto != nil {
            _ = presenter.Board(g.OriginRoom, finishText, dto)
            if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { _ = presenter.Board(g.ResolveRoom, finishText, dto) }
        } else {
            _ = client.SendMessage(context.Background(), g.OriginRoom, finishText)
            if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { _ = client.SendMessage(context.Background(), g.ResolveRoom, finishText) }
        }
        return
    default:
        // Treat as a move
        g, _, err := pvpChessMgr.PlayMove(context.Background(), user, sub)
        if err != nil || g == nil { _ = client.SendMessage(context.Background(), msg.Room, "이동 실패"); return }
        dto, derr := pvpChessMgr.ToDTO(context.Background(), g)
        if derr != nil { _ = client.SendMessage(context.Background(), msg.Room, "표시 오류"); return }
        // Legacy behavior: during play, image only (no text). On finish/draw, include summary text.
        moveText := ""
        if g.Status == pvpchess.StatusFinished {
            winner := g.WhiteName
            if g.Outcome == "black" { winner = g.BlackName }
            moveText = legacyFinishText("checkmate", winner)
        } else if g.Status == pvpchess.StatusDraw {
            moveText = legacyFinishText("draw", "")
        }
        _ = presenter.Board(g.OriginRoom, moveText, dto)
        if g.ResolveRoom != "" && g.ResolveRoom != g.OriginRoom { _ = presenter.Board(g.ResolveRoom, moveText, dto) }
        return
    }
}

// accept/decline commands are intentionally removed (auto-accept policy)

func sanitizeUserArg(s string) string {
    s = strings.TrimSpace(s)
    s = strings.TrimPrefix(s, "@")
    return s
}

func shortID(s string) string {
    s = strings.TrimSpace(s)
    if len(s) <= 8 {
        return s
    }
    return s[:8]
}

func roomAllowed(allowed []string, room string) bool {
    for _, r := range allowed {
        if r == room {
            return true
        }
    }
    return false
}

// Chess command handler
func handleChessCommand(client *irisfast.Client, cfg *appcfg.AppConfig, chess *svcchess.Service, presenter *chesspresenter.Presenter, formatter *chesspresenter.Formatter, msg *irisfast.Message, args []string) {
    meta := svcchess.SessionMeta{
        SessionID: sessionIDFor(msg),
        Room:      msg.Room,
        Sender:    senderName(msg),
    }
    if len(args) == 0 { // help
        _ = client.SendMessage(context.Background(), msg.Room, formatter.Help())
        return
    }
    sub := strings.ToLower(strings.TrimSpace(args[0]))
    ctx := context.Background()

    switch sub {
    case "시작":
        preset := ""
        if len(args) >= 2 { preset = args[1] }
        state, err := chess.StartSession(ctx, meta, preset, false)
        resumed := false
        if err != nil {
            if errorsEqual(err, svcchess.ErrSessionInProgress) {
                // status again to get image/state
        st, sErr := chess.Status(ctx, meta)
                if sErr == nil { state = st; resumed = true; err = nil }
            }
        }
        if err != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "체스 시작 실패: "+err.Error())
            return
        }
        _ = presenter.Board(msg.Room, formatter.Start(chesspresenterAdaptState(state), resumed), chesspresenterAdaptState(state))
    case "현황":
        state, err := chess.Status(ctx, meta)
        if err != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "체스 현황 오류: "+err.Error())
            return
        }
        _ = presenter.Board(msg.Room, formatter.Status(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
    case "무르기":
        state, err := chess.Undo(ctx, meta)
        if err != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "무르기 실패: "+err.Error())
            return
        }
        _ = presenter.Board(msg.Room, formatter.Undo(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
    case "기권":
        state, err := chess.Resign(ctx, meta)
        if err != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "기권 실패: "+err.Error())
            return
        }
        _ = presenter.Board(msg.Room, formatter.Resign(chesspresenterAdaptState(state)), chesspresenterAdaptState(state))
    case "기록":
        limit := 10
        if len(args) >= 2 {
            if n, err := strconv.Atoi(args[1]); err == nil && n > 0 { limit = n }
        }
        games, err := chess.History(ctx, meta, limit)
        if err != nil {
            _ = client.SendMessage(context.Background(), msg.Room, "기록 조회 실패: "+err.Error())
            return
        }
        _ = client.SendMessage(context.Background(), msg.Room, formatter.History(chesspresenter.ToDTOGames(games)))
    case "기보":
        if len(args) < 2 { _ = client.SendMessage(context.Background(), msg.Room, "용법: "+cfg.BotPrefix+"체스 기보 <ID>"); return }
        id, err := strconv.ParseInt(args[1], 10, 64)
        if err != nil { _ = client.SendMessage(context.Background(), msg.Room, "잘못된 ID"); return }
        game, err := chess.Game(ctx, meta, id)
        if err != nil { _ = client.SendMessage(context.Background(), msg.Room, "기보 조회 실패: "+err.Error()); return }
        _ = client.SendMessage(context.Background(), msg.Room, formatter.Game(chesspresenter.ToDTOGame(game)))
    case "프로필":
        profile, err := chess.Profile(ctx, meta)
        if err != nil { _ = client.SendMessage(context.Background(), msg.Room, "프로필 조회 실패: "+err.Error()); return }
        _ = client.SendMessage(context.Background(), msg.Room, formatter.Profile(chesspresenterAdaptProfile(profile)))
    case "선호":
        if len(args) < 2 { _ = client.SendMessage(context.Background(), msg.Room, "용법: "+cfg.BotPrefix+"체스 선호 <preset>"); return }
        profile, err := chess.UpdatePreferredPreset(ctx, meta, args[1])
        if err != nil { _ = client.SendMessage(context.Background(), msg.Room, "선호 난이도 업데이트 실패: "+err.Error()); return }
        _ = client.SendMessage(context.Background(), msg.Room, formatter.PreferredPresetUpdated(chesspresenterAdaptProfile(profile)))
    case "도움":
        suggestion, err := chess.Assist(ctx, meta)
        if err != nil { _ = client.SendMessage(context.Background(), msg.Room, "추천 수 계산 실패: "+err.Error()); return }
        _ = client.SendMessage(context.Background(), msg.Room, formatter.Assist(chesspresenterAdaptAssist(suggestion)))
    default:
        // Treat as a move
        summary, err := chess.Play(ctx, meta, sub)
        if err != nil { _ = client.SendMessage(context.Background(), msg.Room, "이동 실패: "+err.Error()); return }
        dto := chesspresenterAdaptSummary(summary)
        text := formatter.Move(dto)
        // Always draw board even if not finished
        _ = presenter.Board(msg.Room, text, dto.State)
    }
}

// Helpers/adapters (avoid import bleed in main)
func chesspresenterAdaptState(s *svcchess.SessionState) *chessdto.SessionState { return chesspresenter.ToDTOState(s) }
func chesspresenterAdaptSummary(m *svcchess.MoveSummary) *chessdto.MoveSummary { return chesspresenter.ToDTOMoveSummary(m) }
func chesspresenterAdaptProfile(p *domain.ChessProfile) *chessdto.ChessProfile { return chesspresenter.ToDTOProfile(p) }
func chesspresenterAdaptAssist(a *svcchess.AssistSuggestion) *chessdto.AssistSuggestion { return chesspresenter.ToDTOAssist(a) }

func sessionIDFor(msg *irisfast.Message) string {
    uid := userIDFromMessage(msg)
    if uid == "" { uid = senderName(msg) }
    return fmt.Sprintf("%s:%s", strings.TrimSpace(msg.Room), strings.TrimSpace(uid))
}

func senderName(msg *irisfast.Message) string {
    if msg.JSON != nil && strings.TrimSpace(msg.JSON.UserID) != "" {
        return strings.TrimSpace(msg.JSON.UserID)
    }
    if msg.Sender != nil {
        return strings.TrimSpace(*msg.Sender)
    }
    return "player"
}

type prefixProvider struct{ prefix string }
func (p prefixProvider) Prefix() string { return p.prefix }

// simple error compare without direct import cycles
func errorsEqual(err error, target error) bool { return err != nil && target != nil && err.Error() == target.Error() }

// legacyFinishText returns legacy-style summary text for PvP endings.
// event: "checkmate" | "resign" | "draw"
func legacyFinishText(event string, winner string) string {
    switch strings.ToLower(strings.TrimSpace(event)) {
    case "checkmate":
        if strings.TrimSpace(winner) == "" { return "✅ 승리했습니다! 축하드립니다." }
        return fmt.Sprintf("✅ 승리했습니다! 축하드립니다. (승자: %s)", winner)
    case "resign":
        if strings.TrimSpace(winner) == "" { return "🛑 기권하여 패배로 기록되었습니다." }
        return fmt.Sprintf("🛑 기권하여 패배로 기록되었습니다. (승자: %s)", winner)
    case "draw":
        return "🤝 무승부로 종료되었습니다."
    default:
        return ""
    }
}
