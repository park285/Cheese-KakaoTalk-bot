package chessdto

import "time"

type AssistSuggestion struct {
	MoveUCI      string
	MoveSAN      string
	EvaluationCP int
	Principal    []string
	Duration     time.Duration
}

type MoveSummary struct {
	State            *SessionState
	PlayerSAN        string
	PlayerUCI        string
	EngineSAN        string
	EngineUCI        string
	Finished         bool
	GameID           int64
	Profile          *ChessProfile
	RatingDelta      int
	Material         MaterialScore
	Captured         CapturedPieces
	AssistSuggestion *AssistSuggestion
}
