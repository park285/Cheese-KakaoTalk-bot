package pvpchess

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    _ "github.com/lib/pq"
)

type Repository struct {
    db *sql.DB
}

func NewRepository(databaseURL string) (*Repository, error) {
    if strings.TrimSpace(databaseURL) == "" {
        return nil, fmt.Errorf("DATABASE_URL is required")
    }
    db, err := sql.Open("postgres", databaseURL)
    if err != nil {
        return nil, err
    }
    db.SetMaxOpenConns(16)
    db.SetMaxIdleConns(8)
    db.SetConnMaxLifetime(30 * time.Minute)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := db.PingContext(ctx); err != nil {
        return nil, err
    }
    return &Repository{db: db}, nil
}

func (r *Repository) Close() error {
    if r == nil || r.db == nil { return nil }
    return r.db.Close()
}

// SaveResult upserts a final PvP game result into the database.
func (r *Repository) SaveResult(ctx context.Context, g *Game, method string) error {
    if r == nil || r.db == nil || g == nil {
        return nil
    }

    // derive result token and PGN result string
    result := strings.TrimSpace(g.Outcome)
    if result == "resign" {
        // map resign outcome to winner color if possible
        if g.Winner == g.WhiteID { result = "white" } else if g.Winner == g.BlackID { result = "black" } else { result = "" }
    }
    pgnResult := mapResultToPGN(result)

    // build PGN text from SAN list
    pgn := buildPGN(g, pgnResult, method)

    movesUCIRaw, _ := json.Marshal(g.MovesUCI)
    movesSANRaw, _ := json.Marshal(g.MovesSAN)
    duration := g.UpdatedAt.Sub(g.CreatedAt).Milliseconds()
    if duration < 0 { duration = 0 }

    q := `INSERT INTO pvp_games (
        game_id, white_id, white_name, black_id, black_name,
        origin_room, resolve_room, time_control,
        result, result_method, moves_uci, moves_san, pgn,
        started_at, ended_at, duration_ms
      ) VALUES (
        $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16
      ) ON CONFLICT (game_id) DO UPDATE SET
        white_id=EXCLUDED.white_id,
        white_name=EXCLUDED.white_name,
        black_id=EXCLUDED.black_id,
        black_name=EXCLUDED.black_name,
        origin_room=EXCLUDED.origin_room,
        resolve_room=EXCLUDED.resolve_room,
        time_control=EXCLUDED.time_control,
        result=EXCLUDED.result,
        result_method=EXCLUDED.result_method,
        moves_uci=EXCLUDED.moves_uci,
        moves_san=EXCLUDED.moves_san,
        pgn=EXCLUDED.pgn,
        started_at=EXCLUDED.started_at,
        ended_at=EXCLUDED.ended_at,
        duration_ms=EXCLUDED.duration_ms`

    _, err := r.db.ExecContext(ctx, q,
        g.ID,
        g.WhiteID, g.WhiteName,
        g.BlackID, g.BlackName,
        g.OriginRoom, g.ResolveRoom, g.TimeControl,
        result, strings.TrimSpace(method), string(movesUCIRaw), string(movesSANRaw), pgn,
        g.CreatedAt, g.UpdatedAt, duration,
    )
    return err
}

func mapResultToPGN(result string) string {
    switch strings.ToLower(strings.TrimSpace(result)) {
    case "white":
        return "1-0"
    case "black":
        return "0-1"
    case "draw":
        return "1/2-1/2"
    default:
        return "*"
    }
}

func buildPGN(g *Game, pgnResult, method string) string {
    if g == nil {
        return ""
    }
    var b strings.Builder
    date := g.UpdatedAt
    if date.IsZero() {
        date = time.Now()
    }
    // headers
    b.WriteString("[Event \"KakaoPvP\"]\n")
    b.WriteString("[Site \"Iris\"]\n")
    b.WriteString(fmt.Sprintf("[Date \"%04d.%02d.%02d\"]\n", date.Year(), int(date.Month()), date.Day()))
    b.WriteString(fmt.Sprintf("[White \"%s\"]\n", sanitizePGN(g.WhiteName)))
    b.WriteString(fmt.Sprintf("[Black \"%s\"]\n", sanitizePGN(g.BlackName)))
    if strings.TrimSpace(g.TimeControl) != "" {
        b.WriteString(fmt.Sprintf("[TimeControl \"%s\"]\n", sanitizePGN(g.TimeControl)))
    }
    if strings.TrimSpace(method) != "" {
        b.WriteString(fmt.Sprintf("[Termination \"%s\"]\n", sanitizePGN(strings.ToLower(method))))
    }
    b.WriteString(fmt.Sprintf("[Result \"%s\"]\n\n", pgnResult))

    // moves from SAN with numbering
    for i := 0; i < len(g.MovesSAN); i += 2 {
        turn := (i / 2) + 1
        b.WriteString(fmt.Sprintf("%d. %s", turn, strings.TrimSpace(g.MovesSAN[i])))
        if i+1 < len(g.MovesSAN) {
            b.WriteString(" ")
            b.WriteString(strings.TrimSpace(g.MovesSAN[i+1]))
        }
        b.WriteString(" ")
    }
    if pgnResult != "" {
        b.WriteString(pgnResult)
    }
    return b.String()
}

func sanitizePGN(s string) string {
    s = strings.ReplaceAll(s, "\\", " ")
    s = strings.ReplaceAll(s, "\"", "'")
    return strings.TrimSpace(s)
}

