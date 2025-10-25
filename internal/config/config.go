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

	AllowedRooms []string

	StockfishPath         string
	ChessDefaultPreset    string
	ChessSessionTTLSec    int
	ChessHistoryLimit     int
	ChessOpeningMaxPly    int
    ChessOpeningMinWeight int
    ChessOpeningStyle     string

    // When true, run in PvP-only mode: do not initialize single-player engine/service
    PvpOnly bool

    // IgnoreSenders: 표시명이 이 목록에 포함되면 무시
    IgnoreSenders []string

    // START_IMAGE_DELAY_MS: PvP 시작 안내에서 텍스트 후 이미지 전송 전 대기(ms)
    // 기본 150ms
    StartImageDelayMS int

    // FANOUT_IMAGE_DELAY_MS: 방 간 전송 간격(ms) — 이미지 드롭 완화용
    // 기본 200ms
    FanoutImageDelayMS int
}

func Load() (*AppConfig, error) {
    cfg := &AppConfig{
		AllowRandomMatch:   false,
		MaxConcurrentGames: 200,
		ChessDefaultPreset: "level3",
		ChessSessionTTLSec: 3600,
        ChessHistoryLimit:   10,
        StartImageDelayMS:   150,
        FanoutImageDelayMS:  200,
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
    // time control removed

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

    // PvP-only mode (disables single-player engine)
    if v := strings.TrimSpace(os.Getenv("CHESS_PVP_ONLY")); v != "" {
        if b, err := strconv.ParseBool(v); err == nil {
            cfg.PvpOnly = b
        }
    }

    // Optional: ignore specific senders (comma-separated names)
    if v := strings.TrimSpace(os.Getenv("CHESS_IGNORE_SENDERS")); v != "" {
        parts := strings.Split(v, ",")
        for _, p := range parts {
            s := strings.TrimSpace(p)
            if s != "" {
                cfg.IgnoreSenders = append(cfg.IgnoreSenders, s)
            }
        }
    }
    if len(cfg.IgnoreSenders) == 0 { // fallback env key
        if v := strings.TrimSpace(os.Getenv("IGNORE_SENDERS")); v != "" {
            parts := strings.Split(v, ",")
            for _, p := range parts {
                s := strings.TrimSpace(p)
                if s != "" {
                    cfg.IgnoreSenders = append(cfg.IgnoreSenders, s)
                }
            }
        }
    }
    if len(cfg.IgnoreSenders) == 0 {
        // 기본값: Iris(알림/봇 계정) 무시
        cfg.IgnoreSenders = []string{"Iris"}
    }

    // START_IMAGE_DELAY_MS (milliseconds)
    if v := strings.TrimSpace(os.Getenv("START_IMAGE_DELAY_MS")); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n >= 0 {
            cfg.StartImageDelayMS = n
        }
    }

    // FANOUT_IMAGE_DELAY_MS (milliseconds)
    if v := strings.TrimSpace(os.Getenv("FANOUT_IMAGE_DELAY_MS")); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n >= 0 {
            cfg.FanoutImageDelayMS = n
        }
    }

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
