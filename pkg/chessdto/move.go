package chessdto

import "time"

// AssistSuggestion represents a hint produced by the chess engine.
type AssistSuggestion struct {
	MoveUCI      string
	MoveSAN      string
	EvaluationCP int
	Principal    []string
	Duration     time.Duration
}

// MoveSummary summarises player and engine moves after executing a single turn.
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
