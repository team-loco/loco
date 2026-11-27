package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// The routing e2e shard supplies an installed API and an HTTP provider fixture.
// Ordinary module tests have no installed platform and skip this entry point.
func TestPublisherInstalledCore(t *testing.T) {
	path := os.Getenv("LOCO_PUBLISHER_E2E_CONFIG")
	if path == "" {
		t.Skip("run through the routing e2e shard")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Config      config `json:"config"`
		ProviderURL string `json:"providerURL"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	publishers, err := newPublishers(fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	p := publishers[0]
	p.dns.baseURL = fixture.ProviderURL
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		budget := fixture.Config.BatchSize
		if _, err = p.runOnce(t.Context(), fixture.Config, &budget); err != nil {
			t.Fatal(err)
		}
		complete := len(p.publications) > 0
		for _, item := range p.publications {
			if item.nextAttempt.Before(time.Now().Add(time.Duration(fixture.Config.AuditSeconds/2) * time.Second)) {
				complete = false
			}
		}
		if complete {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("installed core publications did not complete")
		case <-ticker.C:
		}
	}
}
