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

	filePath := strings.TrimSpace(getenvDefault("LOG_FILE", filepath.Join("logs", "bot.log")))
	var cores []zapcore.Core

	if console {
		var enc zapcore.Encoder
		switch format {
		case "json":
			enc = zapcore.NewJSONEncoder(jsonEncoderConfig())
		case "console":
			enc = zapcore.NewConsoleEncoder(consoleEncoderConfig(false))
		default:
			enc = zapcore.NewConsoleEncoder(legacyEncoderConfig())
		}
		cores = append(cores, zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level))
	}

	if toFile {
		if err := ensureDir(filepath.Dir(filePath)); err != nil {
			return err
		}
		f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		var fenc zapcore.Encoder
		switch format {
		case "json":
			fenc = zapcore.NewJSONEncoder(jsonEncoderConfig())
		case "console":
			fenc = zapcore.NewConsoleEncoder(consoleEncoderConfig(false))
		default:
			fenc = zapcore.NewConsoleEncoder(legacyEncoderConfig())
		}
		fileCore := zapcore.NewCore(fenc, zapcore.AddSync(f), level)
		cores = append(cores, fileCore)
	}

	if len(cores) == 0 {
		enc := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
		cores = append(cores, zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level))
	}

	logger := zap.New(zapcore.NewTee(cores...))
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
