package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type AppConfig struct {
	IrisBaseURL string
	IrisWSURL   string

	BotPrefix string

	XUserID    string
	XUserEmail string
	XSessionID string

	RedisURL    string
	DatabaseURL string

	AllowRandomMatch   bool
	MaxConcurrentGames int
	TimeControl        string

	AllowedRooms []string

	StockfishPath         string
	ChessDefaultPreset    string
	ChessSessionTTLSec    int
	ChessHistoryLimit     int
	ChessOpeningMaxPly    int
	ChessOpeningMinWeight int
	ChessOpeningStyle     string
}

func Load() (*AppConfig, error) {
	cfg := &AppConfig{
		AllowRandomMatch:   false,
		MaxConcurrentGames: 200,
		TimeControl:        "none",
		ChessDefaultPreset: "level3",
		ChessSessionTTLSec: 3600,
		ChessHistoryLimit:  10,
	}

	cfg.IrisBaseURL = strings.TrimSpace(os.Getenv("IRIS_BASE_URL"))
	cfg.IrisWSURL = strings.TrimSpace(os.Getenv("IRIS_WS_URL"))
	cfg.BotPrefix = strings.TrimSpace(os.Getenv("BOT_PREFIX"))

	cfg.XUserID = strings.TrimSpace(os.Getenv("X_USER_ID"))
	cfg.XUserEmail = strings.TrimSpace(os.Getenv("X_USER_EMAIL"))
	cfg.XSessionID = strings.TrimSpace(os.Getenv("X_SESSION_ID"))

	cfg.RedisURL = strings.TrimSpace(os.Getenv("REDIS_URL"))
	cfg.DatabaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))

	if v := strings.TrimSpace(os.Getenv("ALLOWED_ROOMS")); v != "" {
		parts := strings.Split(v, ",")
		for _, p := range parts {
			s := strings.TrimSpace(p)
			if s != "" {
				cfg.AllowedRooms = append(cfg.AllowedRooms, s)
			}
		}
	}

	if v := strings.TrimSpace(os.Getenv("ALLOW_RANDOM_MATCH")); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			cfg.AllowRandomMatch = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("MAX_CONCURRENT_GAMES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxConcurrentGames = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("TIME_CONTROL")); v != "" {
		cfg.TimeControl = v
	}

	// Chess specific
	cfg.StockfishPath = strings.TrimSpace(os.Getenv("STOCKFISH_PATH"))
	if v := strings.TrimSpace(os.Getenv("CHESS_DEFAULT_PRESET")); v != "" {
		cfg.ChessDefaultPreset = v
	}
	if v := strings.TrimSpace(os.Getenv("CHESS_SESSION_TTL")); v != "" { // seconds or duration like 1h ignored here
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ChessSessionTTLSec = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CHESS_HISTORY_LIMIT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ChessHistoryLimit = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CHESS_OPENING_MAX_PLY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ChessOpeningMaxPly = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CHESS_OPENING_MIN_WEIGHT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ChessOpeningMinWeight = n
		}
	}
	cfg.ChessOpeningStyle = strings.TrimSpace(os.Getenv("CHESS_OPENING_DEFAULT_STYLE"))

	if len(cfg.AllowedRooms) == 0 {
		if v := strings.TrimSpace(os.Getenv("CHESS_ALLOWED_ROOMS")); v != "" {
			parts := strings.Split(v, ",")
			for _, p := range parts {
				s := strings.TrimSpace(p)
				if s != "" {
					cfg.AllowedRooms = append(cfg.AllowedRooms, s)
				}
			}
		}
	}

	if cfg.IrisBaseURL == "" {
		return nil, errors.New("IRIS_BASE_URL is required")
	}
	if cfg.IrisWSURL == "" {
		return nil, errors.New("IRIS_WS_URL is required")
	}
	if cfg.BotPrefix == "" {
		return nil, errors.New("BOT_PREFIX is required")
	}

	return cfg, nil
}
