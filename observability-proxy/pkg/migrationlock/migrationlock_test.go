package migrationlock_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"

	"github.com/team-loco/loco/observability-proxy/internal/testenv"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	testNamespace      = "default"
	leaseNameBytes     = 4
	contenders         = 4
	holdTime           = 100 * time.Millisecond
	testLeaseDuration  = 2 * time.Second
	testRenewDeadline  = time.Second
	testRetryPeriod    = 200 * time.Millisecond
	waitTimeout        = 500 * time.Millisecond
	settleDeadline     = 30 * time.Second
	validLeaseDuration = 15 * time.Second
	validRenewDeadline = 10 * time.Second
	validRetryPeriod   = 2 * time.Second
)

var errMigration = errors.New("migration failed")

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

func validConfig() migrationlock.Config {
	return migrationlock.Config{
		Name:          "loco-obs-schema-migrations",
		Namespace:     "observability",
		Identity:      "loco-obs-obs-proxy-abc",
		LeaseDuration: validLeaseDuration,
		RenewDeadline: validRenewDeadline,
		RetryPeriod:   validRetryPeriod,
	}
}

func TestValidateRejectsInvalidConfig(t *testing.T) {
	cases := map[string]struct {
		mutate func(*migrationlock.Config)
		want   error
	}{
		"empty name": {
			mutate: func(c *migrationlock.Config) { c.Name = "" },
			want:   migrationlock.ErrInvalidName,
		},
		"uppercase name": {
			mutate: func(c *migrationlock.Config) { c.Name = "Schema" },
			want:   migrationlock.ErrInvalidName,
		},
		"empty namespace": {
			mutate: func(c *migrationlock.Config) { c.Namespace = "" },
			want:   migrationlock.ErrInvalidNamespace,
		},
		"empty identity": {
			mutate: func(c *migrationlock.Config) { c.Identity = "" },
			want:   migrationlock.ErrMissingIdentity,
		},
		"zero retry": {
			mutate: func(c *migrationlock.Config) { c.RetryPeriod = 0 },
			want:   migrationlock.ErrNotPositive,
		},
		"renew equals duration": {
			mutate: func(c *migrationlock.Config) { c.RenewDeadline = c.LeaseDuration },
			want:   migrationlock.ErrRenewDeadline,
		},
		"retry exceeds renew": {
			mutate: func(c *migrationlock.Config) { c.RetryPeriod = c.RenewDeadline },
			want:   migrationlock.ErrRetryPeriod,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("Validate() on a valid config = %v", err)
	}
}

type harness struct {
	server *rest.Config
	name   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	server := testenv.APIServer(t)
	suffix := make([]byte, leaseNameBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random lease name: %v", err)
	}
	return &harness{server: server, name: "migrations-" + hex.EncodeToString(suffix)}
}

func coordinationClient(t *testing.T, cfg *rest.Config) *coordinationv1client.CoordinationV1Client {
	t.Helper()
	client, err := coordinationv1client.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("coordination client: %v", err)
	}
	return client
}

func (h *harness) lease(t *testing.T, identity string) *migrationlock.Lease {
	t.Helper()
	return h.leaseVia(t, identity, rest.CopyConfig(h.server))
}

func (h *harness) leaseVia(t *testing.T, identity string, cfg *rest.Config) *migrationlock.Lease {
	t.Helper()
	return migrationlock.New(coordinationClient(t, cfg), migrationlock.Config{
		Name:          h.name,
		Namespace:     testNamespace,
		Identity:      identity,
		LeaseDuration: testLeaseDuration,
		RenewDeadline: testRenewDeadline,
		RetryPeriod:   testRetryPeriod,
	})
}

func (h *harness) holder(t *testing.T) string {
	t.Helper()
	lease, err := coordinationClient(t, h.server).Leases(testNamespace).Get(t.Context(), h.name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lease %s: %v", h.name, err)
	}
	if lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

func TestWithLockRunsOneHolderAtATime(t *testing.T) {
	h := newHarness(t)
	var active, peak atomic.Int32
	errs := make([]error, contenders)
	var wg sync.WaitGroup
	for runner := range contenders {
		lock := h.lease(t, fmt.Sprintf("runner-%d", runner))
		wg.Go(func() {
			errs[runner] = lock.WithLock(t.Context(), func(context.Context) error {
				current := active.Add(1)
				for {
					seen := peak.Load()
					if current <= seen || peak.CompareAndSwap(seen, current) {
						break
					}
				}
				time.Sleep(holdTime)
				active.Add(-1)
				return nil
			})
		})
	}
	wg.Wait()
	for runner, err := range errs {
		if err != nil {
			t.Errorf("runner %d: WithLock: %v", runner, err)
		}
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("%d runners held the lease at once, want 1", got)
	}
}

func TestWithLockReturnsTheMigrationErrorAndReleases(t *testing.T) {
	h := newHarness(t)
	err := h.lease(t, "runner").WithLock(t.Context(), func(context.Context) error {
		return errMigration
	})
	if !errors.Is(err, errMigration) {
		t.Fatalf("WithLock() = %v, want %v", err, errMigration)
	}
	if holder := h.holder(t); holder != "" {
		t.Fatalf("lease holder after WithLock = %q, want released", holder)
	}
}

func holdUntilCancelled(acquired chan<- struct{}) func(context.Context) error {
	return func(ctx context.Context) error {
		close(acquired)
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestWithLockGivesUpWhenContextEndsWhileWaiting(t *testing.T) {
	h := newHarness(t)
	holderCtx, stopHolder := context.WithCancel(t.Context())
	acquired := make(chan struct{})
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- h.lease(t, "holder").WithLock(holderCtx, holdUntilCancelled(acquired))
	}()
	<-acquired

	waitCtx, cancel := context.WithTimeout(t.Context(), waitTimeout)
	defer cancel()
	ran := false
	err := h.lease(t, "waiter").WithLock(waitCtx, func(context.Context) error {
		ran = true
		return nil
	})
	if !errors.Is(err, migrationlock.ErrNotAcquired) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WithLock() = %v, want %v wrapping %v", err, migrationlock.ErrNotAcquired, context.DeadlineExceeded)
	}
	if ran {
		t.Fatal("waiter ran its migration without holding the lease")
	}
	stopHolder()
	if err := <-holderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("holder WithLock() = %v, want %v", err, context.Canceled)
	}
}

type contender struct {
	acquiredAt time.Time
	err        error
}

func waitForSuccessor(t *testing.T, h *harness) <-chan contender {
	t.Helper()
	result := make(chan contender, 1)
	go func() {
		var acquiredAt time.Time
		err := h.lease(t, "successor").WithLock(t.Context(), func(context.Context) error {
			acquiredAt = time.Now()
			return nil
		})
		result <- contender{acquiredAt: acquiredAt, err: err}
	}()
	return result
}

func TestCancelledHolderReleasesTheLease(t *testing.T) {
	h := newHarness(t)
	holderCtx, stopHolder := context.WithCancel(t.Context())
	acquired := make(chan struct{})
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- h.lease(t, "holder").WithLock(holderCtx, holdUntilCancelled(acquired))
	}()
	<-acquired
	successor := waitForSuccessor(t, h)

	stoppedAt := time.Now()
	stopHolder()
	if err := <-holderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("holder WithLock() = %v, want %v", err, context.Canceled)
	}
	select {
	case got := <-successor:
		if got.err != nil {
			t.Fatalf("successor WithLock(): %v", got.err)
		}
		if waited := got.acquiredAt.Sub(stoppedAt); waited >= testLeaseDuration-testRetryPeriod {
			t.Fatalf(
				"successor waited %s after the holder stopped, want less than the %s lease",
				waited,
				testLeaseDuration,
			)
		}
	case <-time.After(settleDeadline):
		t.Fatalf("successor did not acquire within %s", settleDeadline)
	}
}

func TestDeadHolderLeaseIsTakenAfterItExpires(t *testing.T) {
	h := newHarness(t)
	var network testenv.Switch
	holderCtx, killHolder := context.WithCancel(t.Context())
	acquired := make(chan struct{})
	holderErr := make(chan error, 1)
	var holderStoppedAt atomic.Int64
	holderLock := h.leaseVia(t, "holder", network.Wrap(rest.CopyConfig(h.server)))
	go func() {
		holderErr <- holderLock.WithLock(holderCtx, func(ctx context.Context) error {
			close(acquired)
			<-ctx.Done()
			holderStoppedAt.Store(time.Now().UnixNano())
			return ctx.Err()
		})
	}()
	<-acquired
	successor := waitForSuccessor(t, h)

	diedAt := time.Now()
	network.Cut()
	killHolder()
	if err := <-holderErr; err == nil {
		t.Fatal("dead holder WithLock() = nil, want an error")
	}
	if holder := h.holder(t); holder != "holder" {
		t.Fatalf("lease holder right after the holder died = %q, want the dead holder", holder)
	}
	select {
	case got := <-successor:
		if got.err != nil {
			t.Fatalf("successor WithLock(): %v", got.err)
		}
		waited := got.acquiredAt.Sub(diedAt)
		if waited < testLeaseDuration-testRetryPeriod {
			t.Fatalf(
				"successor acquired %s after the holder died, before the %s lease expired",
				waited,
				testLeaseDuration,
			)
		}
		if stopped := time.Unix(0, holderStoppedAt.Load()); !stopped.Before(got.acquiredAt) {
			t.Fatalf("successor acquired at %s while the dead holder ran until %s", got.acquiredAt, stopped)
		}
		t.Logf("successor acquired %s after the holder died (lease %s)", waited, testLeaseDuration)
	case <-time.After(settleDeadline):
		t.Fatalf("successor did not acquire within %s", settleDeadline)
	}
}

func TestDisconnectedHolderStopsBeforeTheLeaseExpires(t *testing.T) {
	h := newHarness(t)
	var network testenv.Switch
	acquired := make(chan struct{})
	holderErr := make(chan error, 1)
	var holderStoppedAt atomic.Int64
	holderLock := h.leaseVia(t, "holder", network.Wrap(rest.CopyConfig(h.server)))
	go func() {
		holderErr <- holderLock.WithLock(t.Context(), func(ctx context.Context) error {
			close(acquired)
			<-ctx.Done()
			holderStoppedAt.Store(time.Now().UnixNano())
			return nil
		})
	}()
	<-acquired
	successor := waitForSuccessor(t, h)

	network.Cut()
	if err := <-holderErr; !errors.Is(err, migrationlock.ErrLeaseLost) {
		t.Fatalf("disconnected holder WithLock() = %v, want %v", err, migrationlock.ErrLeaseLost)
	}
	got := <-successor
	if got.err != nil {
		t.Fatalf("successor WithLock(): %v", got.err)
	}
	if stopped := time.Unix(0, holderStoppedAt.Load()); !stopped.Before(got.acquiredAt) {
		t.Fatalf("successor acquired at %s while the disconnected holder ran until %s", got.acquiredAt, stopped)
	}
}
