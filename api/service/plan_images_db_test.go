package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/go-containerregistry/pkg/name"

	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const slowLookup = 300 * time.Millisecond

// slowResolver takes slowLookup per image, or until the context ends.
type slowResolver struct{}

func (slowResolver) Resolve(ctx context.Context, _ name.Reference) (string, error) {
	select {
	case <-time.After(slowLookup):
		return testDigest, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// stuckResolver never answers before the context ends.
type stuckResolver struct{}

func (stuckResolver) Resolve(ctx context.Context, _ name.Reference) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func imageFile(services int) string {
	var file strings.Builder
	file.WriteString(planFileHeader)
	for i := range services {
		fmt.Fprintf(&file, `  svc%d:
    image: nginx:1.%d
    port: 8080
    regions:
      us-ca-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`, i, i)
	}
	return file.String()
}

type imagePlan struct {
	resolver ImageResolver
	counts   *configv1.CountLimits
	timeout  time.Duration
	services int
}

func planImages(t *testing.T, f *deployFixture, p imagePlan) (*planv1.PlanResponse, error) {
	t.Helper()
	server := NewPlanServer(
		f.pool, f.queries, p.resolver, testRegistryHost, testServiceDefaults(),
		testSecretConfig(), testClusterSilentAfter, p.counts,
	)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: f.workspaceReadScopes(t)})
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return server.Plan(ctx, &planv1.PlanRequest{File: []byte(imageFile(p.services)), EnvironmentId: f.envID.String()})
}

func TestPlanResolvesManyImagesWithinTheDeadline(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	const services = 20

	// One lookup after another would take services*slowLookup, twice the deadline.
	resp, err := planImages(t, f, imagePlan{
		resolver: slowResolver{}, counts: limits.Defaults(), timeout: services * slowLookup / 2, services: services,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetErrors()) != 0 {
		t.Fatalf("errors = %v, want none", resp.GetErrors())
	}
	if len(resp.GetImages()) != services {
		t.Fatalf("images = %d, want %d", len(resp.GetImages()), services)
	}
}

func TestPlanThatRunsOutOfTimeSaysSo(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)

	_, err := planImages(t, f, imagePlan{
		resolver: stuckResolver{}, counts: limits.Defaults(), timeout: 2 * time.Second, services: 1,
	})
	if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatalf("Plan = %v, want DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "ran out of time") {
		t.Fatalf("Plan = %v, want a message that the plan ran out of time", err)
	}
}

func TestPlanRefusesAFileOverTheServiceLimitBeforeResolvingImages(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	counts := limits.Defaults()
	limits.ServicesPerFile.Set(counts, 2)
	resolver := &fakeResolver{digest: testDigest}

	_, err := planImages(t, f, imagePlan{resolver: resolver, counts: counts, timeout: 10 * time.Second, services: 3})
	if !errors.Is(err, limits.ErrLimitReached) {
		t.Fatalf("Plan = %v, want %v", err, limits.ErrLimitReached)
	}
	if !strings.Contains(err.Error(), "3 services and the limit is 2") {
		t.Fatalf("Plan = %v, want the count and the limit", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolved %v, want no lookups", resolver.calls)
	}

	if _, err := planImages(t, f, imagePlan{
		resolver: resolver, counts: counts, timeout: 10 * time.Second, services: 2,
	}); err != nil {
		t.Fatalf("Plan at the limit: %v", err)
	}
}
