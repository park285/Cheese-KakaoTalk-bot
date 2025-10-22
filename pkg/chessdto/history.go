package chessdto

import "time"

type ChessGame struct {
	ID            int64
	SessionUUID   string
	PlayerHash    string
	RoomHash      string
	Preset        string
	EnginePreset  string
	Result        string
	ResultMethod  string
	MovesUCI      []string
	MovesSAN      []string
	PGN           string
	StartedAt     time.Time
	EndedAt       time.Time
	Duration      time.Duration
	Blunders      int
	EngineLatency time.Duration
}
