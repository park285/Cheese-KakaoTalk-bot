package pvpchess

import (
	"time"
)

// Color identifies chess side.
type Color string

const (
	White Color = "white"
	Black Color = "black"
)

// Status represents a PvP game lifecycle state.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusFinished Status = "FINISHED"
	StatusResigned Status = "RESIGNED"
	StatusDraw     Status = "DRAW"
	StatusAborted  Status = "ABORTED"
)

// Game is the persisted state of a PvP match.
type Game struct {
	ID          string    `json:"id"`
	FEN         string    `json:"fen"`
	MovesUCI    []string  `json:"moves_uci"`
	MovesSAN    []string  `json:"moves_san"`
	Turn        Color     `json:"turn"`
	Status      Status    `json:"status"`
	WhiteID     string    `json:"white_id"`
	WhiteName   string    `json:"white_name"`
	BlackID     string    `json:"black_id"`
	BlackName   string    `json:"black_name"`
	OriginRoom  string    `json:"origin_room"`
	ResolveRoom string    `json:"resolve_room"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Winner      string    `json:"winner,omitempty"`
	Outcome     string    `json:"outcome,omitempty"`
}
