package obslog

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "go.uber.org/zap"
    "go.uber.org/zap/zapcore"
)

// 간단 전역 로거 초기화. 콘솔+파일 동시 출력 지원.
// 환경변수:
// - LOG_LEVEL: info(default)|debug|warn|error
// - LOG_FORMAT: legacy(default)|console|json
//   legacy: 레거시 봇과 동일한 ConsoleEncoder(시간: 2006-01-02 15:04:05, 대문자 레벨, 구분자 ' | ').
// - LOG_FILE: 로그 파일 경로 (기본 logs/bot.log)
// - LOG_TO_CONSOLE: true(default)|false
// - LOG_TO_FILE: true(default)|false
// - LOG_CALLER: false(default)|true

var (
    globalLogger *zap.Logger = zap.NewNop()
)

// L는 전역 로거를 반환.
func L() *zap.Logger { return globalLogger }

// InitFromEnv는 환경설정으로 zap 로거를 초기화.
func InitFromEnv() error {
    level := parseLevel(getenvDefault("LOG_LEVEL", "info"))
    console := strings.EqualFold(getenvDefault("LOG_TO_CONSOLE", "true"), "true")
    toFile := strings.EqualFold(getenvDefault("LOG_TO_FILE", "true"), "true")
    showCaller := strings.EqualFold(getenvDefault("LOG_CALLER", "false"), "true")
    format := strings.ToLower(strings.TrimSpace(getenvDefault("LOG_FORMAT", "legacy")))
    if format != "legacy" && format != "json" && format != "console" {
        format = "legacy"
    }

    // 파일 설정
    filePath := strings.TrimSpace(getenvDefault("LOG_FILE", filepath.Join("logs", "bot.log")))
    var cores []zapcore.Core

    // 콘솔 코어
    if console {
        var enc zapcore.Encoder
        switch format {
        case "json":
            enc = zapcore.NewJSONEncoder(jsonEncoderConfig())
        case "console":
            enc = zapcore.NewConsoleEncoder(consoleEncoderConfig(false))
        default: // legacy
            enc = zapcore.NewConsoleEncoder(legacyEncoderConfig())
        }
        cores = append(cores, zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level))
    }

    // 파일 코어
    if toFile {
        if err := ensureDir(filepath.Dir(filePath)); err != nil {
            return err
        }
        f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
        if err != nil {
            return fmt.Errorf("open log file: %w", err)
        }
        // 파일 인코더: legacy는 ConsoleEncoder, json/console 선택 반영
        var fenc zapcore.Encoder
        switch format {
        case "json":
            fenc = zapcore.NewJSONEncoder(jsonEncoderConfig())
        case "console":
            fenc = zapcore.NewConsoleEncoder(consoleEncoderConfig(false))
        default: // legacy
            fenc = zapcore.NewConsoleEncoder(legacyEncoderConfig())
        }
        fileCore := zapcore.NewCore(fenc, zapcore.AddSync(f), level)
        cores = append(cores, fileCore)
    }

    if len(cores) == 0 {
        // 안전장치: 콘솔 기본
        enc := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
        cores = append(cores, zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level))
    }

    logger := zap.New(zapcore.NewTee(cores...))
    // legacy 포맷은 레거시와 동일하게 caller+stacktrace(error) 강제 활성화
    if format == "legacy" {
        showCaller = true
    }
    if showCaller {
        logger = logger.WithOptions(zap.AddCaller())
    }
    logger = logger.WithOptions(zap.AddStacktrace(zapcore.ErrorLevel))
    globalLogger = logger
    return nil
}

func ensureDir(dir string) error {
    if strings.TrimSpace(dir) == "" || dir == "." {
        return nil
    }
    if _, err := os.Stat(dir); err == nil {
        return nil
    }
    return os.MkdirAll(dir, 0o755)
}

func parseLevel(s string) zapcore.Level {
    switch strings.ToLower(strings.TrimSpace(s)) {
    case "debug":
        return zapcore.DebugLevel
    case "warn", "warning":
        return zapcore.WarnLevel
    case "error":
        return zapcore.ErrorLevel
    case "dpanic":
        return zapcore.DPanicLevel
    case "panic":
        return zapcore.PanicLevel
    case "fatal":
        return zapcore.FatalLevel
    default:
        return zapcore.InfoLevel
    }
}

func getenvDefault(k, def string) string {
    v := os.Getenv(k)
    if strings.TrimSpace(v) == "" {
        return def
    }
    return v
}

// 인코더 설정들
func legacyEncoderConfig() zapcore.EncoderConfig {
    cfg := zap.NewProductionEncoderConfig()
    cfg.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
    cfg.EncodeLevel = zapcore.CapitalLevelEncoder
    cfg.ConsoleSeparator = " | "
    return cfg
}

func consoleEncoderConfig(color bool) zapcore.EncoderConfig {
    cfg := zap.NewProductionEncoderConfig()
    cfg.EncodeTime = zapcore.ISO8601TimeEncoder
    if color {
        cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
    } else {
        cfg.EncodeLevel = zapcore.CapitalLevelEncoder
    }
    return cfg
}

func jsonEncoderConfig() zapcore.EncoderConfig {
    cfg := zap.NewProductionEncoderConfig()
    cfg.EncodeTime = zapcore.ISO8601TimeEncoder
    cfg.EncodeLevel = zapcore.LowercaseLevelEncoder
    return cfg
}
