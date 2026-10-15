// Package common is a minimal replacement for ragflow/internal/common,
// exposing only the logger seam and string helpers the LLM-wiki knowledge
// compiler uses.
package common

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var logger *zap.Logger

func init() {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	logger, _ = cfg.Build()
}

// SetLevel overrides the global log level (e.g. to debug).
func SetLevel(level zapcore.Level) {
	if logger != nil {
		logger = logger.WithOptions(zap.IncreaseLevel(level))
	}
}

// Info logs at info level.
func Info(msg string, fields ...zap.Field) { logger.Info(msg, fields...) }

// Error logs at error level.
func Error(msg string, err error, fields ...zap.Field) {
	f := append([]zap.Field{zap.Error(err)}, fields...)
	logger.Error(msg, f...)
}

// Debug logs at debug level.
func Debug(msg string, fields ...zap.Field) { logger.Debug(msg, fields...) }

// Warn logs at warn level.
func Warn(msg string, fields ...zap.Field) { logger.Warn(msg, fields...) }

// IsTransientError replicates ragflow/internal/common IsTransientError: 5xx and
// 429 are transient; other 4xx permanent; context deadline / network transient;
// context cancelled permanent.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if idx := strings.Index(msg, "status "); idx >= 0 {
		code := 0
		for _, r := range msg[idx+len("status "):] {
			if r < '0' || r > '9' {
				break
			}
			code = code*10 + int(r-'0')
		}
		if code >= 500 || code == 429 {
			return true
		}
		if code >= 400 {
			return false
		}
	}
	return true
}

// StripThinkTrailing cuts everything through the LAST " response" marker and
// returns what follows (parity with ragflow/internal/common).
func StripThinkTrailing(s string) string {
	if i := strings.LastIndex(s, " response"); i >= 0 {
		return s[i+len(" response"):]
	}
	return s
}
