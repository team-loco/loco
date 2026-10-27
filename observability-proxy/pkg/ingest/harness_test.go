package ingest

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	testClusterID        = "0199e0a4-0000-7000-8000-0000000000c1"
	testOtherWorkspaceID = "22222222-3333-4444-5555-666666666666"
	testTokenLifetime    = time.Hour
	testPlacedFor        = time.Hour
	testClockSkew        = time.Minute
	testAuthTimeout      = 5 * time.Second
	testJWKSRefresh      = time.Hour
	testPlacementTTL     = time.Hour
	testPlacementRefetch = time.Nanosecond
	testRetryAfter       = 30 * time.Second
	testCurrentKeyID     = "current"
	testRotatedKeyID     = "rotated"
)

type controlPlane struct {
	*ingesttest.ControlPlane
	t *testing.T
}

func newSigningKey(t *testing.T, id string) ingesttest.Signer {
	t.Helper()
	signer, err := ingesttest.NewSigner(id)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func newControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	fake := ingesttest.Start([]ingesttest.Signer{newSigningKey(t, testCurrentKeyID)})
	t.Cleanup(fake.Close)
	return &controlPlane{ControlPlane: fake, t: t}
}

func (c *controlPlane) token(issuedAt time.Time) string {
	c.t.Helper()
	token, _, err := ingesttoken.Sign(c.t.Context(), c.SigningKey(), testClusterID, issuedAt, testTokenLifetime)
	if err != nil {
		c.t.Fatal(err)
	}
	return token
}

func (c *controlPlane) bearer() string {
	c.t.Helper()
	return bearerPrefix + c.token(time.Now())
}

func (c *controlPlane) gate() *Gate {
	return NewGate(GateConfig{
		ControlPlaneURL:     c.URL(),
		RequestTimeout:      testAuthTimeout,
		JWKSRefreshInterval: testJWKSRefresh,
		PlacementTTL:        testPlacementTTL,
		RefetchInterval:     testPlacementRefetch,
		ClockSkew:           testClockSkew,
	})
}

func newGatedService(consumers Consumers, cp *controlPlane) *Service {
	cfg := ServiceConfig{MaxRequestBytes: testMaxRequestBytes, RetryAfter: testRetryAfter}
	return NewService(consumers, cp.gate(), cfg, prometheus.NewRegistry())
}

func newPlacedControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	cp := newControlPlane(t)
	cp.Place(testWorkspaceID, time.Now().Add(-testPlacedFor), time.Time{})
	return cp
}
