package pvpchess

import (
    "context"
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
        TimeControl: strings.TrimSpace(timeControl),
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }

    if err := m.save(ctx, g); err != nil { return nil, err }
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

// PlayMove applies a move for the requesting user (UCI preferred, fallback to SAN).
func (m *Manager) PlayMove(ctx context.Context, userID, moveStr string) (*Game, string, error) {
    if strings.TrimSpace(userID) == "" { return nil, "", fmt.Errorf("invalid user") }
    g, err := m.GetActiveGameByUser(ctx, userID)
    if err != nil || g == nil { return nil, "", err }

    // ensure it's player's turn
    playerColor := m.playerColor(g, userID)
    if playerColor == "" { return nil, "", fmt.Errorf("user not in game") }
    if (g.Turn == White && playerColor != "white") || (g.Turn == Black && playerColor != "black") {
        return g, "지금은 상대 차례입니다.", nil
    }

    // reconstruct game from fen+moves
    game := reconstruct(g.FEN, g.MovesUCI)
    if game == nil { return nil, "", fmt.Errorf("failed to reconstruct game") }

    pos := game.Position()
    uci := strings.ToLower(strings.TrimSpace(moveStr))
    if uci == "" { return g, "잘못된 수 입력입니다.", nil }

    // try UCI first
    notationUCI := nchess.UCINotation{}
    if mv, derr := notationUCI.Decode(pos, uci); derr == nil {
        game.Move(mv, nil)
        // capture SAN from the played move
        san := nchess.AlgebraicNotation{}.Encode(pos, mv)
        g.MovesUCI = append(g.MovesUCI, uci)
        g.MovesSAN = append(g.MovesSAN, san)
    } else {
        // fallback SAN
        if err := game.PushNotationMove(uci, nchess.AlgebraicNotation{}, nil); err != nil {
            return g, "유효하지 않은 수입니다.", nil
        }
        // derive both SAN and UCI from last move
        last := lastMove(game)
        if last == nil { return g, "유효하지 않은 수입니다.", nil }
        g.MovesSAN = append(g.MovesSAN, nchess.AlgebraicNotation{}.Encode(pos, last))
        g.MovesUCI = append(g.MovesUCI, last.String())
    }

    g.FEN = game.FEN()
    g.Turn = colorFrom(game.Position().Turn())
    g.UpdatedAt = time.Now()

    switch game.Outcome() {
    case nchess.WhiteWon:
        g.Status = StatusFinished
        g.Winner = g.WhiteID
        g.Outcome = "white"
    case nchess.BlackWon:
        g.Status = StatusFinished
        g.Winner = g.BlackID
        g.Outcome = "black"
    case nchess.Draw:
        g.Status = StatusDraw
        g.Outcome = "draw"
    }

    if err := m.save(ctx, g); err != nil { return nil, "", err }
    // Persist final results (checkmate/draw) when game is no longer active
    if g.Status == StatusFinished {
        _ = m.persistIfFinal(ctx, g, "checkmate")
    } else if g.Status == StatusDraw {
        _ = m.persistIfFinal(ctx, g, "draw")
    }

    who := g.WhiteName
    if playerColor == "black" { who = g.BlackName }
    return g, fmt.Sprintf("%s: %s", who, strings.TrimSpace(moveStr)), nil
}

func (m *Manager) Resign(ctx context.Context, userID string) (*Game, string, error) {
    g, err := m.GetActiveGameByUser(ctx, userID)
    if err != nil || g == nil { return nil, "", err }
    g.Status = StatusResigned
    g.Winner = opponentID(g, userID)
    g.Outcome = "resign"
    g.UpdatedAt = time.Now()
    if err := m.save(ctx, g); err != nil { return nil, "", err }
    _ = m.persistIfFinal(ctx, g, "resignation")
    return g, "기권", nil
}

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

func reconstruct(fen string, moves []string) *nchess.Game {
    var game *nchess.Game
    if strings.TrimSpace(fen) == "" || fen == "startpos" {
        game = nchess.NewGame()
    } else {
        option, err := nchess.FEN(fen)
        if err != nil { return nil }
        game = nchess.NewGame(option)
    }
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

func (m *Manager) indexParticipants(ctx context.Context, id string, white, black string) error {
    if strings.TrimSpace(white) != "" {
        if err := m.rdb.SAdd(ctx, idxUserKey(white), id).Err(); err != nil { return err }
    }
    if strings.TrimSpace(black) != "" {
        if err := m.rdb.SAdd(ctx, idxUserKey(black), id).Err(); err != nil { return err }
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
    return m.repo.SaveResult(ctx, g, method)
}
