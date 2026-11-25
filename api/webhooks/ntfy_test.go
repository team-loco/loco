package webhooks

import (
	"errors"
	"slices"
	"testing"
)

const testTopic = "alerts"

func TestDefaultNtfyEventTypes(t *testing.T) {
	t.Parallel()
	want := []string{
		"build.failed", "deployment.failed", "deployment.crashed",
		"cluster.silent", "cluster.recovered", "cluster.health_changed", "org.suspended",
	}
	if !slices.Equal(DefaultNtfyEventTypes, want) {
		t.Fatalf("default event types = %v, want %v", DefaultNtfyEventTypes, want)
	}
}

func TestNewNtfyConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, server, topic string
		wantURL             string
		wantErr             error
	}{
		{name: "off", wantURL: ""},
		{
			name:    "trailing slash",
			server:  "https://ntfy.test/",
			topic:   testTopic,
			wantURL: "https://ntfy.test/" + testTopic,
		},
		{
			name:    "path prefix",
			server:  "https://host.test/ntfy",
			topic:   testTopic,
			wantURL: "https://host.test/ntfy/" + testTopic,
		},
		{name: "topic only", topic: testTopic, wantErr: ErrNtfyUnset},
		{name: "query", server: "https://ntfy.test/?x=1", topic: testTopic, wantErr: ErrNtfyServer},
		{name: "fragment", server: "https://ntfy.test/#x", topic: testTopic, wantErr: ErrNtfyServer},
		{name: "credentials", server: "https://user@ntfy.test", topic: testTopic, wantErr: ErrNtfyServer},
		{name: "scheme", server: "ftp://ntfy.test", topic: testTopic, wantErr: ErrNtfyServer},
		{name: "topic with a slash", server: "https://ntfy.test", topic: "a/b", wantErr: ErrNtfyTopic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := NewNtfyConfig(tt.server, tt.topic, "", nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantURL == "" {
				return
			}
			if cfg.TopicURL != tt.wantURL {
				t.Fatalf("topic URL = %q, want %q", cfg.TopicURL, tt.wantURL)
			}
		})
	}
}
