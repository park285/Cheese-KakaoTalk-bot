package chessbuilder

import (
    "context"
    "database/sql"
    "fmt"
    "net/url"
    "strconv"
    "strings"
    "time"

    _ "github.com/lib/pq"
    corechess "github.com/park285/Cheese-KakaoTalk-bot/internal/chess"
    "github.com/park285/Cheese-KakaoTalk-bot/internal/config"
    "github.com/park285/Cheese-KakaoTalk-bot/internal/service/cache"
    svcchess "github.com/park285/Cheese-KakaoTalk-bot/internal/service/chess"
    "go.uber.org/zap"
)

type Deps struct {
    Service *svcchess.Service
    Engine  *corechess.Engine
    Cache   *cache.CacheService
    Repo    svcchess.Repository
}

func New(cfg *config.AppConfig, logger *zap.Logger) (*Deps, error) {
    if cfg == nil {
        return nil, fmt.Errorf("nil config")
    }
    if logger == nil {
        logger = zap.NewNop()
    }

    if strings.TrimSpace(cfg.StockfishPath) == "" {
        return nil, fmt.Errorf("STOCKFISH_PATH is required for chess engine")
    }

    // Engine
    engine, err := corechess.NewEngine(cfg.StockfishPath)
    if err != nil {
        return nil, fmt.Errorf("init engine: %w", err)
    }
    // Opening prefs (optional)
    if cfg.ChessOpeningMaxPly > 0 || cfg.ChessOpeningMinWeight > 0 {
        engine.SetOpeningOptions(corechess.OpeningOptions{MaxPly: cfg.ChessOpeningMaxPly, MinWeight: cfg.ChessOpeningMinWeight})
    }

    // Cache (Redis optional)
    var cacheSvc *cache.CacheService
    if strings.TrimSpace(cfg.RedisURL) != "" {
        cconf, perr := parseRedisURL(cfg.RedisURL)
        if perr != nil {
            return nil, fmt.Errorf("parse redis url: %w", perr)
        }
        cacheSvc, err = cache.NewCacheService(*cconf, logger)
        if err != nil {
            return nil, fmt.Errorf("init cache: %w", err)
        }
    } else {
        // Fallback: minimal in-process cache shim without Redis is not implemented; chess service requires CacheService.
        // For local development, provide a Redis container.
        return nil, fmt.Errorf("REDIS_URL is required for chess sessions/cache")
    }

    // Repository (DB required)
    if strings.TrimSpace(cfg.DatabaseURL) == "" {
        return nil, fmt.Errorf("DATABASE_URL is required for chess repository")
    }
    db, err := sql.Open("postgres", cfg.DatabaseURL)
    if err != nil {
        return nil, fmt.Errorf("open postgres: %w", err)
    }
    // basic pool settings
    db.SetMaxOpenConns(16)
    db.SetMaxIdleConns(8)
    db.SetConnMaxLifetime(30 * time.Minute)

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := db.PingContext(ctx); err != nil {
        return nil, fmt.Errorf("ping postgres: %w", err)
    }
    repo := svcchess.NewRepository(db)

    // Allowed rooms: use global ALLOWED_ROOMS only (config loader handles compatibility)
    allowed := cfg.AllowedRooms

    svcCfg := svcchess.Config{
        DefaultPreset:       cfg.ChessDefaultPreset,
        SessionTTL:          time.Duration(cfg.ChessSessionTTLSec) * time.Second,
        HistoryLimit:        cfg.ChessHistoryLimit,
        AllowedRooms:        append([]string(nil), allowed...),
        DefaultOpeningStyle: strings.TrimSpace(cfg.ChessOpeningStyle),
    }

    service, err := svcchess.NewService(engine, cacheSvc, repo, svcchess.NewSVGBoardRenderer(), svcCfg, logger)
    if err != nil {
        return nil, err
    }

    return &Deps{Service: service, Engine: engine, Cache: cacheSvc, Repo: repo}, nil
}

func parseRedisURL(raw string) (*cache.CacheConfig, error) {
    u, err := url.Parse(raw)
    if err != nil {
        return nil, err
    }
    if u.Scheme != "redis" && u.Scheme != "rediss" {
        return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
    }
    host := u.Hostname()
    portStr := u.Port()
    if portStr == "" {
        portStr = "6379"
    }
    port, err := strconv.Atoi(portStr)
    if err != nil {
        return nil, err
    }
    db := 0
    if u.Path != "" {
        p := strings.TrimPrefix(u.Path, "/")
        if p != "" {
            if n, err := strconv.Atoi(p); err == nil {
                db = n
            }
        }
    }
    pass, _ := u.User.Password()
    return &cache.CacheConfig{Host: host, Port: port, Password: pass, DB: db}, nil
}
