package logging

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNew(t *testing.T) {
	for _, format := range []string{"console", "json"} {
		logger, err := New(format, "debug")
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		logger.Debug("test")
		_ = logger.Sync()
	}
	if _, err := New("text", "info"); err == nil {
		t.Fatal("accepted invalid format")
	}
	if _, err := New("json", "loud"); err == nil {
		t.Fatal("accepted invalid level")
	}
}

func TestRedactsSecretsInMessagesAndFields(t *testing.T) {
	var output bytes.Buffer
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zapcore.DebugLevel)
	logger := zap.New(wrapRedaction(core)).With(zap.String("saved", "sk-project_secret123"))
	logger.Error("request sk-message_secret123 failed",
		zap.String("key", "sk-field_secret123"),
		zap.Error(errors.New("bad sk-error_secret123")),
		zap.Reflect("payload", map[string]string{"token": "sk-reflect_secret123"}),
	)
	logged := output.String()
	if strings.Contains(logged, "secret123") {
		t.Fatalf("secret leaked in log: %s", logged)
	}
	if count := strings.Count(logged, "[redacted]"); count != 5 {
		t.Fatalf("redactions = %d in %s", count, logged)
	}
}
