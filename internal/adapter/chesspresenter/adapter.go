package chesspresenter

import (
    nchess "github.com/corentings/chess/v2"
    svc "github.com/kapu/kakao-cheese-bot-go/internal/service/chess"
    "github.com/kapu/kakao-cheese-bot-go/internal/domain"
    "github.com/kapu/kakao-cheese-bot-go/pkg/chessdto"
)

func ToDTOState(s *svc.SessionState) *chessdto.SessionState {
    if s == nil {
        return nil
    }
    return &chessdto.SessionState{
        SessionUUID: s.SessionUUID,
        Preset:      s.Preset,
        MovesSAN:    append([]string(nil), s.MovesSAN...),
        MovesUCI:    append([]string(nil), s.Moves...),
        FEN:         s.FEN,
        BoardImage:  append([]byte(nil), s.BoardImage...),
        MoveCount:   s.MoveCount,
        Material:    chessdto.MaterialScore{White: s.Material.White, Black: s.Material.Black},
        Captured:    toDTOCaptured(s.Captured),
        AutoAssist:  s.AutoAssist,
        Profile:     ToDTOProfile(s.Profile),
        RatingDelta: s.RatingDelta,
        Outcome:     s.Outcome.String(),
        OutcomeMeta: s.OutcomeMethod.String(),
        GameID:      0,
    }
}

func ToDTOMoveSummary(m *svc.MoveSummary) *chessdto.MoveSummary {
    if m == nil {
        return nil
    }
    return &chessdto.MoveSummary{
        State:            ToDTOState(m.State),
        PlayerSAN:        m.PlayerSAN,
        PlayerUCI:        m.PlayerUCI,
        EngineSAN:        m.EngineSAN,
        EngineUCI:        m.EngineUCI,
        Finished:         m.Finished,
        GameID:           m.GameID,
        Profile:          ToDTOProfile(m.Profile),
        RatingDelta:      m.RatingDelta,
        Material:         chessdto.MaterialScore{White: m.Material.White, Black: m.Material.Black},
        Captured:         toDTOCaptured(m.Captured),
        AssistSuggestion: ToDTOAssist(m.AssistSuggestion),
    }
}

func ToDTOAssist(a *svc.AssistSuggestion) *chessdto.AssistSuggestion {
    if a == nil {
        return nil
    }
    return &chessdto.AssistSuggestion{
        MoveUCI:      a.MoveUCI,
        MoveSAN:      a.MoveSAN,
        EvaluationCP: a.EvaluationCP,
        Principal:    append([]string(nil), a.Principal...),
        Duration:     a.Duration,
    }
}

func toDTOCaptured(c svc.CapturedPieces) chessdto.CapturedPieces {
    return chessdto.CapturedPieces{
        White: toPieceTokenList(c.WhiteOrder),
        Black: toPieceTokenList(c.BlackOrder),
    }
}

func toPieceTokenList(list []nchess.PieceType) []string {
    tokens := make([]string, 0, len(list))
    for _, pt := range list {
        tokens = append(tokens, pieceTypeToToken(pt))
    }
    return tokens
}

func pieceTypeToToken(pt nchess.PieceType) string {
    switch pt {
    case nchess.Queen:
        return "queen"
    case nchess.Rook:
        return "rook"
    case nchess.Bishop:
        return "bishop"
    case nchess.Knight:
        return "knight"
    case nchess.Pawn:
        return "pawn"
    case nchess.King:
        return "king"
    default:
        return ""
    }
}

// profile
func ToDTOProfile(p *domain.ChessProfile) *chessdto.ChessProfile {
    if p == nil {
        return nil
    }
    cp := *p
    return &chessdto.ChessProfile{
        PlayerHash:      cp.PlayerHash,
        RoomHash:        cp.RoomHash,
        PreferredPreset: cp.PreferredPreset,
        Rating:          cp.Rating,
        GamesPlayed:     cp.GamesPlayed,
        Wins:            cp.Wins,
        Losses:          cp.Losses,
        Draws:           cp.Draws,
        Streak:          cp.Streak,
        StreakType:      cp.StreakType,
        LastPreset:      cp.LastPreset,
        LastPlayedAt:    cp.LastPlayedAt,
        UpdatedAt:       cp.UpdatedAt,
        CreatedAt:       cp.CreatedAt,
    }
}

func ToDTOGames(list []*domain.ChessGame) []*chessdto.ChessGame {
    out := make([]*chessdto.ChessGame, 0, len(list))
    for _, g := range list {
        if g == nil {
            continue
        }
        gg := *g
        out = append(out, &chessdto.ChessGame{
            ID:            gg.ID,
            SessionUUID:   gg.SessionUUID,
            PlayerHash:    gg.PlayerHash,
            RoomHash:      gg.RoomHash,
            Preset:        gg.Preset,
            EnginePreset:  gg.EnginePreset,
            Result:        gg.Result,
            ResultMethod:  gg.ResultMethod,
            MovesUCI:      append([]string(nil), gg.MovesUCI...),
            MovesSAN:      append([]string(nil), gg.MovesSAN...),
            PGN:           gg.PGN,
            StartedAt:     gg.StartedAt,
            EndedAt:       gg.EndedAt,
            Duration:      gg.Duration,
            Blunders:      gg.Blunders,
            EngineLatency: gg.EngineLatency,
        })
    }
    return out
}

func ToDTOGame(g *domain.ChessGame) *chessdto.ChessGame {
    if g == nil { return nil }
    gg := *g
    return &chessdto.ChessGame{
        ID:            gg.ID,
        SessionUUID:   gg.SessionUUID,
        PlayerHash:    gg.PlayerHash,
        RoomHash:      gg.RoomHash,
        Preset:        gg.Preset,
        EnginePreset:  gg.EnginePreset,
        Result:        gg.Result,
        ResultMethod:  gg.ResultMethod,
        MovesUCI:      append([]string(nil), gg.MovesUCI...),
        MovesSAN:      append([]string(nil), gg.MovesSAN...),
        PGN:           gg.PGN,
        StartedAt:     gg.StartedAt,
        EndedAt:       gg.EndedAt,
        Duration:      gg.Duration,
        Blunders:      gg.Blunders,
        EngineLatency: gg.EngineLatency,
    }
}
