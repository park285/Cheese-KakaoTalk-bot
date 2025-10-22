package pvp

import (
	"strings"
	"time"
)

type ColorChoice string

const (
	ColorWhite  ColorChoice = "white"
	ColorBlack  ColorChoice = "black"
	ColorRandom ColorChoice = "random"
)

func ParseColorChoice(s string) ColorChoice {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "white", "w":
		return ColorWhite
	case "black", "b":
		return ColorBlack
	default:
		return ColorRandom
	}
}

type Status string

const (
	StatusPending  Status = "PENDING"
	StatusAccepted Status = "ACCEPTED"
	StatusDeclined Status = "DECLINED"
)

type Challenge struct {
	ID           string
	OriginRoom   string
	ResolveRoom  string
	ChallengerID string
	TargetID     string
	Color        ColorChoice
	TimeControl  string
	CreatedAt    time.Time
	Status       Status
}
