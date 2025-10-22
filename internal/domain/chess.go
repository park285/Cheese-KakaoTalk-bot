package domain

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

type ChessProfile struct {
	PlayerHash      string
	RoomHash        string
	PreferredPreset string
	Rating          int
	GamesPlayed     int
	Wins            int
	Losses          int
	Draws           int
	Streak          int
	StreakType      string
	LastPreset      string
	LastPlayedAt    time.Time
	UpdatedAt       time.Time
	CreatedAt       time.Time
}
