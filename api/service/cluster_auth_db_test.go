package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/tvm"
)

func TestClusterAssertionDoesNotWaitOnExpiredAssertions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	insertExpiredAssertion(t, f, f.otherCluster)

	// An open transaction holding the expired row's lock stands in for a concurrent sweep.
	sweep, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sweep.Rollback(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := sweep.Exec(t.Context(), `DELETE FROM cluster_assertions WHERE expires_at < NOW()`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	clusters := NewClusterAuthenticator(f.queries, testAgentConfig().AssertionMaxLifetime)
	if _, err := clusters.Authenticate(ctx, "Bearer "+testAssertion(t, f.clusterID)); err != nil {
		t.Fatalf("Authenticate while another transaction deletes expired assertions: %v", err)
	}
}

func TestClusterAssertionIsAcceptedOnce(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	clusters := NewClusterAuthenticator(f.queries, testAgentConfig().AssertionMaxLifetime)
	header := "Bearer " + testAssertion(t, f.clusterID)
	if _, err := clusters.Authenticate(t.Context(), header); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if _, err := clusters.Authenticate(t.Context(), header); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("replay = %v, want %v", err, ErrInvalidAgentToken)
	}
}

func TestTokenCleanupSweepsExpiredClusterAssertions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	clusters := NewClusterAuthenticator(f.queries, testAgentConfig().AssertionMaxLifetime)
	if _, err := clusters.Authenticate(t.Context(), "Bearer "+testAssertion(t, f.clusterID)); err != nil {
		t.Fatal(err)
	}
	insertExpiredAssertion(t, f, f.otherCluster)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		tvm.NewVendingMachine(f.queries, tvm.Config{}).RunCleanup(ctx, time.Millisecond, 1)
		close(done)
	}()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(5 * time.Second)
	for assertionCount(t, f, f.otherCluster) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("expired assertion was not swept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if live := assertionCount(t, f, f.clusterID); live != 1 {
		t.Fatalf("live assertions = %d, want 1", live)
	}
}

func insertExpiredAssertion(t *testing.T, f *deployFixture, clusterID uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(),
		`INSERT INTO cluster_assertions (cluster_id, jti, expires_at) VALUES ($1, $2, NOW() - INTERVAL '1 hour')`,
		clusterID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func assertionCount(t *testing.T, f *deployFixture, clusterID uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM cluster_assertions WHERE cluster_id = $1`, clusterID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
