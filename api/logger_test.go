package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestCustomHandlerDropsSecretAttributes(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(CustomHandler{Handler: slog.NewJSONHandler(&out, nil)})
	logger.With("token", "tok_live").Info("set secrets",
		"environmentId", "env-1",
		"values", map[string]string{"STRIPE_KEY": "sk_live"},
		"names", []string{"STRIPE_KEY"},
	)
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	for _, key := range []string{"values", "token"} {
		if _, present := line[key]; present {
			t.Errorf("log line %q carries %q", out.String(), key)
		}
	}
	for _, key := range []string{"environmentId", "names"} {
		if _, present := line[key]; !present {
			t.Errorf("log line %q lost %q", out.String(), key)
		}
	}
}
