package pvpchess

import (
    "context"
    "errors"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "math/big"
    "net/url"
    "sort"
    "strconv"
    "strings"
    "time"

    nchess "github.com/corentings/chess/v2"
    "github.com/redis/go-redis/v9"
    svcchess "github.com/park285/Cheese-KakaoTalk-bot/internal/service/chess"
    "github.com/park285/Cheese-KakaoTalk-bot/internal/obslog"
    "go.uber.org/zap"
)

type Manager struct {
    rdb      *redis.Client
    renderer svcchess.BoardRenderer
    repo     *Repository
}

func NewManager(redisURL string) (*Manager, error) {
    if strings.TrimSpace(redisURL) == "" {
        return nil, fmt.Errorf("REDIS_URL required for PvP manager")
    }
    opts, err := parseRedisURL(redisURL)
    if err != nil { return nil, err }
    rdb := redis.NewClient(opts)
    if err := rdb.Ping(context.Background()).Err(); err != nil {
        return nil, fmt.Errorf("redis ping: %w", err)
    }
    return &Manager{rdb: rdb, renderer: svcchess.NewSVGBoardRenderer()}, nil
}

func (m *Manager) Close() error {
    if m == nil || m.rdb == nil { return nil }
    return m.rdb.Close()
}

// AttachRepository wires a database repository for persisting PvP results.
func (m *Manager) AttachRepository(r *Repository) {
    if m != nil {
        m.repo = r
    }
}

// CreateGameFromChallenge creates a PvP game from a challenge with auto-accept outcome.
func (m *Manager) CreateGameFromChallenge(ctx context.Context, originRoom, resolveRoom, challengerID, challengerName, targetID, targetName, colorChoice, timeControl string) (*Game, error) {
    if m == nil || m.rdb == nil { return nil, fmt.Errorf("pvp manager not initialized") }
    if challengerID == "" || targetID == "" { return nil, fmt.Errorf("invalid participants") }

    // assign colors
    whiteID, whiteName := challengerID, challengerName
    blackID, blackName := targetID, targetName
    cv := strings.ToLower(strings.TrimSpace(colorChoice))
    switch cv {
    case "white", "w":
        // challenger already white
    case "black", "b":
        whiteID, whiteName, blackID, blackName = targetID, targetName, challengerID, challengerName
    default: // random using crypto/rand
        if n, _ := rand.Int(rand.Reader, big.NewInt(2)); n != nil && n.Int64() == 0 {
            whiteID, whiteName, blackID, blackName = targetID, targetName, challengerID, challengerName
        }
    }

    g := &Game{
        ID:          fmt.Sprintf("pvp-%d-%s", time.Now().UnixNano(), secureRandSuffix(3)),
        FEN:         "startpos",
        MovesUCI:    []string{},
        MovesSAN:    []string{},
        Turn:        White,
        Status:      StatusActive,
        WhiteID:     strings.TrimSpace(whiteID),
        WhiteName:   strings.TrimSpace(whiteName),
        BlackID:     strings.TrimSpace(blackID),
        BlackName:   strings.TrimSpace(blackName),
        OriginRoom:  strings.TrimSpace(originRoom),
        ResolveRoom: strings.TrimSpace(resolveRoom),
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }

    if err := m.save(ctx, g); err != nil { return nil, err }
    obslog.L().Info("pvp_game_create",
        zap.String("game_id", g.ID),
        zap.String("origin_room", g.OriginRoom),
        zap.String("resolve_room", g.ResolveRoom),
        zap.String("white_id", g.WhiteID),
        zap.String("black_id", g.BlackID),
    )
    if err := m.indexParticipants(ctx, g.ID, g.WhiteID, g.BlackID); err != nil { return nil, err }
    return g, nil
}

// GetActiveGameByUser returns the latest active game for a user.
func (m *Manager) GetActiveGameByUser(ctx context.Context, userID string) (*Game, error) {
    if m == nil || m.rdb == nil { return nil, fmt.Errorf("pvp manager not initialized") }
    if strings.TrimSpace(userID) == "" { return nil, nil }
    key := idxUserKey(userID)
    ids, err := m.rdb.SMembers(ctx, key).Result()
    if err != nil { return nil, err }
    if len(ids) == 0 { return nil, nil }
    // Prefer most recently updated
    var list []*Game
    for _, id := range ids {
        g, gerr := m.get(ctx, id)
        if gerr == nil && g != nil && g.Status == StatusActive {
            list = append(list, g)
        }
    }
    if len(list) == 0 { return nil, nil }
    sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
    return list[0], nil
}

// GetActiveGameByUserInRoom returns the most recent ACTIVE game for the user in the given room.
// 방 기준 중복 대국 금지 정책 구현을 위해 사용.
func (m *Manager) GetActiveGameByUserInRoom(ctx context.Context, userID, room string) (*Game, error) {
    if m == nil || m.rdb == nil { return nil, fmt.Errorf("pvp manager not initialized") }
    userID = strings.TrimSpace(userID)
    room = strings.TrimSpace(room)
    if userID == "" || room == "" { return nil, nil }
    key := idxUserKey(userID)
    ids, err := m.rdb.SMembers(ctx, key).Result()
    if err != nil { return nil, err }
    if len(ids) == 0 { return nil, nil }
    var list []*Game
    for _, id := range ids {
        g, gerr := m.get(ctx, id)
        if gerr != nil || g == nil { continue }
        if g.Status != StatusActive { continue }
        if g.OriginRoom == room || g.ResolveRoom == room {
            list = append(list, g)
        }
    }
    if len(list) == 0 { return nil, nil }
    sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
    return list[0], nil
}

// PlayMove applies a move for the requesting user (UCI preferred, fallback to SAN).
func (m *Manager) PlayMove(ctx context.Context, userID, moveStr string) (*Game, string, error) {
    if strings.TrimSpace(userID) == "" { return nil, "", fmt.Errorf("invalid user") }
    g, err := m.GetActiveGameByUser(ctx, userID)
    if err != nil || g == nil { return nil, "", err }

    // optimistic concurrency control using WATCH on game key
    gameK := gameKey(g.ID)
    oldLen := len(g.MovesUCI)

    var resultText string
    // sentinels for flow control
    var (
        errNotYourTurn = errors.New("not_your_turn")
        errIllegalMove = errors.New("illegal_move")
    )

    err = m.rdb.Watch(ctx, func(tx *redis.Tx) error {
        raw, err := tx.Get(ctx, gameK).Bytes()
        if err == redis.Nil {
            return fmt.Errorf("game not found")
        }
        if err != nil { return err }
        var cur Game
        if jerr := json.Unmarshal(raw, &cur); jerr != nil { return jerr }
        if cur.Status != StatusActive { return redis.TxFailedErr }
        // Ensure no concurrent move applied
        if len(cur.MovesUCI) != oldLen { return redis.TxFailedErr }

        // validate turn
        playerColor := m.playerColor(&cur, userID)
        if playerColor == "" { return fmt.Errorf("user not in game") }
        if (cur.Turn == White && playerColor != "white") || (cur.Turn == Black && playerColor != "black") {
            return errNotYourTurn
        }

        // reconstruct and apply move
        game := reconstruct(cur.FEN, cur.MovesUCI)
        if game == nil { return fmt.Errorf("failed to reconstruct game") }
        pos := game.Position()
        rawMove := strings.TrimSpace(moveStr)
        uci := strings.ToLower(rawMove)
        if uci == "" { resultText = "잘못된 수 입력입니다."; return errIllegalMove }

        notationUCI := nchess.UCINotation{}
        if mv, derr := notationUCI.Decode(pos, uci); derr == nil {
            game.Move(mv, nil)
            san := nchess.AlgebraicNotation{}.Encode(pos, mv)
            cur.MovesUCI = append(cur.MovesUCI, uci)
            cur.MovesSAN = append(cur.MovesSAN, san)
        } else {
            if err := game.PushNotationMove(rawMove, nchess.AlgebraicNotation{}, nil); err != nil {
                resultText = "유효하지 않은 수입니다."
                return errIllegalMove
            }
            last := lastMove(game)
            if last == nil { resultText = "유효하지 않은 수입니다."; return errIllegalMove }
            cur.MovesSAN = append(cur.MovesSAN, nchess.AlgebraicNotation{}.Encode(pos, last))
            cur.MovesUCI = append(cur.MovesUCI, last.String())
        }

        cur.FEN = game.FEN()
        cur.Turn = colorFrom(game.Position().Turn())
        cur.UpdatedAt = time.Now()

        switch game.Outcome() {
        case nchess.WhiteWon:
            cur.Status = StatusFinished
            cur.Winner = cur.WhiteID
            cur.Outcome = "white"
        case nchess.BlackWon:
            cur.Status = StatusFinished
            cur.Winner = cur.BlackID
            cur.Outcome = "black"
        case nchess.Draw:
            cur.Status = StatusDraw
            cur.Outcome = "draw"
        }

        // persist atomically
        pipe := tx.TxPipeline()
        newRaw, _ := json.Marshal(&cur)
        pipe.Set(ctx, gameK, newRaw, 24*time.Hour)
        if _, err := pipe.Exec(ctx); err != nil { return err }

        // publish result to outer scope
        g = &cur
        who := g.WhiteName
        if playerColor == "black" { who = g.BlackName }
        resultText = fmt.Sprintf("%s: %s", who, strings.TrimSpace(moveStr))
        return nil
    }, gameK)

    if err != nil {
        if errors.Is(err, redis.TxFailedErr) {
            // concurrent update detected
            return g, "동시 명령이 감지되어 처리되지 않았습니다. 다시 시도해주세요.", nil
        }
        if err.Error() == "illegal_move" || errors.Is(err, errors.New("illegal_move")) { // fallback check
            if strings.TrimSpace(resultText) == "" { resultText = "유효하지 않은 수입니다." }
            return g, resultText, nil
        }
        if err.Error() == "not_your_turn" || errors.Is(err, errors.New("not_your_turn")) {
            return g, "지금은 상대 차례입니다.", nil
        }
        return nil, "", err
    }

    obslog.L().Info("pvp_move",
        zap.String("game_id", g.ID),
        zap.String("user_id", strings.TrimSpace(userID)),
        zap.String("turn", func() string { if g.Turn == White { return "white" }; return "black" }()),
        zap.String("last_uci", func() string { if n := len(g.MovesUCI); n > 0 { return g.MovesUCI[n-1] }; return "" }()),
        zap.String("status", string(g.Status)),
        zap.String("outcome", g.Outcome),
    )
    // Persist final results (checkmate/draw) when game is no longer active
    if g.Status == StatusFinished {
        _ = m.persistIfFinal(ctx, g, "checkmate")
    } else if g.Status == StatusDraw {
        _ = m.persistIfFinal(ctx, g, "draw")
    }

    return g, resultText, nil
}

// PlayMoveByRoom applies a move but restricts target selection to the user's ACTIVE game in the given room.
// 왜: 동일 사용자가 여러 방에서 PvP를 동시 진행할 때 현재 방 외 게임에 수가 적용되는 문제를 방지.
func (m *Manager) PlayMoveByRoom(ctx context.Context, userID, roomID, moveStr string) (*Game, string, error) {
    if strings.TrimSpace(userID) == "" || strings.TrimSpace(roomID) == "" {
        return nil, "", fmt.Errorf("invalid parameters")
    }
    g, err := m.GetActiveGameByUserInRoom(ctx, userID, roomID)
    if err != nil || g == nil { return nil, "", err }

    gameK := gameKey(g.ID)
    oldLen := len(g.MovesUCI)
    var resultText string
    var (
        errNotYourTurn = errors.New("not_your_turn")
        errIllegalMove = errors.New("illegal_move")
    )
    err = m.rdb.Watch(ctx, func(tx *redis.Tx) error {
        raw, err := tx.Get(ctx, gameK).Bytes()
        if err == redis.Nil { return fmt.Errorf("game not found") }
        if err != nil { return err }
        var cur Game
        if jerr := json.Unmarshal(raw, &cur); jerr != nil { return jerr }
        if cur.Status != StatusActive { return redis.TxFailedErr }
        if len(cur.MovesUCI) != oldLen { return redis.TxFailedErr }
        // 스코프 확인: 여전히 같은 방이어야 함
        if !(strings.TrimSpace(cur.OriginRoom) == strings.TrimSpace(roomID) || strings.TrimSpace(cur.ResolveRoom) == strings.TrimSpace(roomID)) {
            return fmt.Errorf("game not in room")
        }
        // 턴 검증
        playerColor := m.playerColor(&cur, userID)
        if playerColor == "" { return fmt.Errorf("user not in game") }
        if (cur.Turn == White && playerColor != "white") || (cur.Turn == Black && playerColor != "black") {
            return errNotYourTurn
        }
        // 재구성 + 적용
        game := reconstruct(cur.FEN, cur.MovesUCI)
        if game == nil { return fmt.Errorf("failed to reconstruct game") }
        pos := game.Position()
        rawMove := strings.TrimSpace(moveStr)
        uci := strings.ToLower(rawMove)
        if uci == "" { resultText = "잘못된 수 입력입니다."; return errIllegalMove }
        notationUCI := nchess.UCINotation{}
        if mv, derr := notationUCI.Decode(pos, uci); derr == nil {
            game.Move(mv, nil)
            san := nchess.AlgebraicNotation{}.Encode(pos, mv)
            cur.MovesUCI = append(cur.MovesUCI, uci)
            cur.MovesSAN = append(cur.MovesSAN, san)
        } else {
            if err := game.PushNotationMove(rawMove, nchess.AlgebraicNotation{}, nil); err != nil {
                resultText = "유효하지 않은 수입니다."
                return errIllegalMove
            }
            last := lastMove(game)
            if last == nil { resultText = "유효하지 않은 수입니다."; return errIllegalMove }
            cur.MovesSAN = append(cur.MovesSAN, nchess.AlgebraicNotation{}.Encode(pos, last))
            cur.MovesUCI = append(cur.MovesUCI, last.String())
        }
        cur.FEN = game.FEN()
        cur.Turn = colorFrom(game.Position().Turn())
        cur.UpdatedAt = time.Now()
        switch game.Outcome() {
        case nchess.WhiteWon:
            cur.Status = StatusFinished
            cur.Winner = cur.WhiteID
            cur.Outcome = "white"
        case nchess.BlackWon:
            cur.Status = StatusFinished
            cur.Winner = cur.BlackID
            cur.Outcome = "black"
        case nchess.Draw:
            cur.Status = StatusDraw
            cur.Outcome = "draw"
        }
        pipe := tx.TxPipeline()
        newRaw, _ := json.Marshal(&cur)
        pipe.Set(ctx, gameK, newRaw, 24*time.Hour)
        if _, err := pipe.Exec(ctx); err != nil { return err }
        g = &cur
        who := g.WhiteName
        if m.playerColor(g, userID) == "black" { who = g.BlackName }
        resultText = fmt.Sprintf("%s: %s", who, strings.TrimSpace(moveStr))
        return nil
    }, gameK)
    if err != nil {
        if errors.Is(err, redis.TxFailedErr) {
            return g, "동시 명령이 감지되어 처리되지 않았습니다. 다시 시도해주세요.", nil
        }
        if err.Error() == "illegal_move" || errors.Is(err, errors.New("illegal_move")) {
            if strings.TrimSpace(resultText) == "" { resultText = "유효하지 않은 수입니다." }
            return g, resultText, nil
        }
        if err.Error() == "not_your_turn" || errors.Is(err, errors.New("not_your_turn")) {
            return g, "지금은 상대 차례입니다.", nil
        }
        return nil, "", err
    }
    obslog.L().Info("pvp_move_by_room",
        zap.String("game_id", g.ID),
        zap.String("room_id", strings.TrimSpace(roomID)),
        zap.String("user_id", strings.TrimSpace(userID)),
        zap.String("turn", func() string { if g.Turn == White { return "white" }; return "black" }()),
        zap.String("last_uci", func() string { if n := len(g.MovesUCI); n > 0 { return g.MovesUCI[n-1] }; return "" }()),
        zap.String("status", string(g.Status)),
        zap.String("outcome", g.Outcome),
    )
    if g.Status == StatusFinished {
        _ = m.persistIfFinal(ctx, g, "checkmate")
    } else if g.Status == StatusDraw {
        _ = m.persistIfFinal(ctx, g, "draw")
    }
    return g, resultText, nil
}

func (m *Manager) Resign(ctx context.Context, userID string) (*Game, string, error) {
    g, err := m.GetActiveGameByUser(ctx, userID)
    if err != nil || g == nil { return nil, "", err }
    gameK := gameKey(g.ID)
    err = m.rdb.Watch(ctx, func(tx *redis.Tx) error {
        raw, err := tx.Get(ctx, gameK).Bytes()
        if err == redis.Nil { return fmt.Errorf("game not found") }
        if err != nil { return err }
        var cur Game
        if jerr := json.Unmarshal(raw, &cur); jerr != nil { return jerr }
        if cur.Status != StatusActive { return redis.TxFailedErr }
        cur.Status = StatusResigned
        cur.Winner = opponentID(&cur, userID)
        cur.Outcome = "resign"
        cur.UpdatedAt = time.Now()
        pipe := tx.TxPipeline()
        newRaw, _ := json.Marshal(&cur)
        pipe.Set(ctx, gameK, newRaw, 24*time.Hour)
        if _, err := pipe.Exec(ctx); err != nil { return err }
        g = &cur
        return nil
    }, gameK)
    if err != nil {
        if errors.Is(err, redis.TxFailedErr) {
            return nil, "", fmt.Errorf("game no longer active")
        }
        return nil, "", err
    }
    obslog.L().Info("pvp_resign",
        zap.String("game_id", g.ID),
        zap.String("resigner", strings.TrimSpace(userID)),
        zap.String("winner", g.Winner),
    )
    _ = m.persistIfFinal(ctx, g, "resignation")
    return g, "기권", nil
}

// ResignByRoom resigns the active game for a user limited to a specific room scope.
// 방 스코프 모호성 제거: 같은 유저가 여러 방에서 동시 PvP를 진행하더라도 정확히 해당 방의 게임만 기권 처리.
func (m *Manager) ResignByRoom(ctx context.Context, userID, roomID string) (*Game, string, error) {
    if strings.TrimSpace(userID) == "" || strings.TrimSpace(roomID) == "" {
        return nil, "", fmt.Errorf("invalid parameters")
    }
    g, err := m.GetActiveGameByUserInRoom(ctx, userID, roomID)
    if err != nil || g == nil { return nil, "", err }
    gameK := gameKey(g.ID)
    err = m.rdb.Watch(ctx, func(tx *redis.Tx) error {
        raw, err := tx.Get(ctx, gameK).Bytes()
        if err == redis.Nil { return fmt.Errorf("game not found") }
        if err != nil { return err }
        var cur Game
        if jerr := json.Unmarshal(raw, &cur); jerr != nil { return jerr }
        if cur.Status != StatusActive { return redis.TxFailedErr }
        // ensure room scope still matches
        if !(cur.OriginRoom == roomID || cur.ResolveRoom == roomID) {
            return fmt.Errorf("game not in room")
        }
        cur.Status = StatusResigned
        cur.Winner = opponentID(&cur, userID)
        cur.Outcome = "resign"
        cur.UpdatedAt = time.Now()
        pipe := tx.TxPipeline()
        newRaw, _ := json.Marshal(&cur)
        pipe.Set(ctx, gameK, newRaw, 24*time.Hour)
        if _, err := pipe.Exec(ctx); err != nil { return err }
        g = &cur
        return nil
    }, gameK)
    if err != nil {
        if errors.Is(err, redis.TxFailedErr) {
            return nil, "", fmt.Errorf("game no longer active")
        }
        return nil, "", err
    }
    obslog.L().Info("pvp_resign_by_room",
        zap.String("game_id", g.ID),
        zap.String("resigner", strings.TrimSpace(userID)),
        zap.String("room_id", strings.TrimSpace(roomID)),
        zap.String("winner", g.Winner),
    )
    _ = m.persistIfFinal(ctx, g, "resignation")
    return g, "기권", nil
}

// 수동 무승부(제안/수락) 기능 제거됨: 규칙상 자동 무승부만 허용

// 중단(Abort) 기능 제거됨: 정책에 따라 지원하지 않음

// Helpers
func opponentID(g *Game, userID string) string {
    if g.WhiteID == userID { return g.BlackID }
    if g.BlackID == userID { return g.WhiteID }
    return ""
}

func (m *Manager) playerColor(g *Game, userID string) string {
    if g.WhiteID == userID { return "white" }
    if g.BlackID == userID { return "black" }
    return ""
}

func lastMove(game *nchess.Game) *nchess.Move {
    moves := game.Moves()
    if len(moves) == 0 { return nil }
    return moves[len(moves)-1]
}

func reconstruct(_ string, moves []string) *nchess.Game {
    // Always reconstruct from start position by applying stored UCI moves.
    // FEN is maintained on Game for presentation; applying it here can double-apply moves.
    game := nchess.NewGame()
    for _, mv := range moves {
        if err := game.PushNotationMove(mv, nchess.UCINotation{}, nil); err != nil { return nil }
    }
    return game
}

func colorFrom(c nchess.Color) Color { if c == nchess.White { return White }; return Black }

// Persistence
func (m *Manager) save(ctx context.Context, g *Game) error {
    raw, err := json.Marshal(g)
    if err != nil { return err }
    if err := m.rdb.Set(ctx, gameKey(g.ID), raw, 24*time.Hour).Err(); err != nil { return err }
    return nil
}

func (m *Manager) get(ctx context.Context, id string) (*Game, error) {
    raw, err := m.rdb.Get(ctx, gameKey(id)).Bytes()
    if err == redis.Nil { return nil, nil }
    if err != nil { return nil, err }
    var g Game
    if err := json.Unmarshal(raw, &g); err != nil { return nil, err }
    return &g, nil
}

// LoadGame returns the game by ID. 공개 래퍼(get) — 호출자는 상태 확인용으로 사용.
func (m *Manager) LoadGame(ctx context.Context, id string) (*Game, error) {
    return m.get(ctx, id)
}

func (m *Manager) indexParticipants(ctx context.Context, id string, white, black string) error {
    if strings.TrimSpace(white) != "" {
        key := idxUserKey(white)
        if err := m.rdb.SAdd(ctx, key, id).Err(); err != nil { return err }
        // 인덱스 키 TTL도 갱신하여 누적 방지(게임 TTL과 동일)
        _ = m.rdb.Expire(ctx, key, 24*time.Hour).Err()
    }
    if strings.TrimSpace(black) != "" {
        key := idxUserKey(black)
        if err := m.rdb.SAdd(ctx, key, id).Err(); err != nil { return err }
        _ = m.rdb.Expire(ctx, key, 24*time.Hour).Err()
    }
    return nil
}

func gameKey(id string) string { return "pvp:game:" + strings.TrimSpace(id) }
func idxUserKey(userID string) string { return "pvp:index:user:" + strings.TrimSpace(userID) }

func parseRedisURL(raw string) (*redis.Options, error) {
    u, err := url.Parse(raw)
    if err != nil { return nil, err }
    if u.Scheme != "redis" && u.Scheme != "rediss" { return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme) }
    db := 0
    if p := strings.TrimPrefix(u.Path, "/"); p != "" { if n, err := strconv.Atoi(p); err == nil { db = n } }
    pass, _ := u.User.Password()
    return &redis.Options{Addr: u.Host, Password: pass, DB: db}, nil
}

// ParseRedisURLForChan returns address, password, and db extracted from REDIS_URL.
// This is provided for wiring the channel manager without exposing redis.Options type upstream.
func ParseRedisURLForChan(raw string) (addr, password string, db int, err error) {
    u, e := url.Parse(raw)
    if e != nil { err = e; return }
    if u.Scheme != "redis" && u.Scheme != "rediss" { err = fmt.Errorf("unsupported scheme: %s", u.Scheme); return }
    if p := strings.TrimPrefix(u.Path, "/"); p != "" { if n, e2 := strconv.Atoi(p); e2 == nil { db = n } }
    password, _ = u.User.Password()
    addr = u.Host
    return
}

// secureRandSuffix returns a hex string of n bytes length; falls back to timestamp-based when crypto fails.
func secureRandSuffix(n int) string {
    if n <= 0 { n = 3 }
    b := make([]byte, n)
    if _, err := rand.Read(b); err == nil {
        return hex.EncodeToString(b)
    }
    // fallback
    return fmt.Sprintf("%x", time.Now().UnixNano()%1_000_000)
}

// persistIfFinal saves the final game result to repository if available.
func (m *Manager) persistIfFinal(ctx context.Context, g *Game, method string) error {
    if m == nil || m.repo == nil || g == nil {
        return nil
    }
    if g.Status != StatusFinished && g.Status != StatusResigned && g.Status != StatusDraw {
        return nil
    }
    if err := m.repo.SaveResult(ctx, g, method); err != nil {
        obslog.L().Error("pvp_result_persist_error", zap.String("game_id", g.ID), zap.String("outcome", g.Outcome), zap.Error(err))
        return err
    }
    obslog.L().Info("pvp_result_persist", zap.String("game_id", g.ID), zap.String("outcome", g.Outcome), zap.String("method", method))
    return nil
}

// time control removed: parsing disabled
