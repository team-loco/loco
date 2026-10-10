package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactAttrDropsSecretBearingKeys(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{ReplaceAttr: redactAttr}))
	logger.Info("applied", "placement_id", "p1", "data", map[string][]byte{"KEY": []byte("plain-value")})

	line := out.String()
	if strings.Contains(line, "plain-value") || strings.Contains(line, `"data"`) {
		t.Fatalf("log line carries the secret data: %s", line)
	}
	if !strings.Contains(line, `"placement_id":"p1"`) {
		t.Fatalf("log line lost the placement id: %s", line)
	}
}
