package chessdto

import "time"

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
