// Package logging configures Zap for human-readable development logs or
// machine-readable production logs with mandatory secret redaction.
package logging

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var secretPattern = regexp.MustCompile(`(?i)sk-[a-z0-9_-]{4,}`)

// New returns a stderr logger using console or JSON encoding at level.
// Redaction wraps the finished core so it applies to messages, per-call fields,
// and fields attached later with logger.With.
func New(format, level string) (*zap.Logger, error) {
	var parsed zapcore.Level
	if err := parsed.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
		return nil, err
	}
	var cfg zap.Config
	switch format {
	case "console":
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	case "json":
		cfg = zap.NewProductionConfig()
	default:
		return nil, errors.New("log format must be console or json")
	}
	cfg.Level = zap.NewAtomicLevelAt(parsed)
	cfg.OutputPaths = []string{"stderr"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	logger, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return logger.WithOptions(zap.WrapCore(wrapRedaction)), nil
}

type redactingCore struct{ zapcore.Core }

func wrapRedaction(core zapcore.Core) zapcore.Core { return redactingCore{Core: core} }

func (core redactingCore) With(fields []zapcore.Field) zapcore.Core {
	return redactingCore{Core: core.Core.With(redactFields(fields))}
}

func (core redactingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !core.Enabled(entry.Level) {
		return checked
	}
	entry.Message = redact(entry.Message)
	return checked.AddCore(entry, core)
}

func (core redactingCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	entry.Message = redact(entry.Message)
	return core.Core.Write(entry, redactFields(fields))
}

func redactFields(fields []zapcore.Field) []zapcore.Field {
	// Copy before editing because Zap callers may reuse their field slice.
	redacted := make([]zapcore.Field, len(fields))
	copy(redacted, fields)
	for i := range redacted {
		switch redacted[i].Type {
		case zapcore.StringType:
			redacted[i].String = redact(redacted[i].String)
		case zapcore.ErrorType:
			if err, ok := redacted[i].Interface.(error); ok {
				redacted[i].Interface = errors.New(redact(err.Error()))
			}
		case zapcore.ReflectType:
			// Reflection fields can hide strings inside arbitrary values. Rendering
			// them first favors confidentiality over preserving structured shape.
			redacted[i].Type = zapcore.StringType
			redacted[i].String = redact(fmt.Sprint(redacted[i].Interface))
			redacted[i].Interface = nil
		}
	}
	return redacted
}

func redact(value string) string { return secretPattern.ReplaceAllString(value, "[redacted]") }
