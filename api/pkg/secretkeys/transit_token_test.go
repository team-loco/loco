package secretkeys

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testTokenTTL      = time.Second
	testRenewMargin   = 900 * time.Millisecond
	testRenewRetry    = 20 * time.Millisecond
	testWaitDeadline  = 5 * time.Second
	testWaitPoll      = 5 * time.Millisecond
	testQuietPeriod   = 200 * time.Millisecond
	testClockLeap     = time.Hour
	wantRenewals      = 2
	wantFailedRenewal = 3
)

func startRenewal(t *testing.T, transit *Transit) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		transit.renewToken(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(testWaitDeadline)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(testWaitPoll)
	}
}

func TestTransitRenewsTheTokenBeforeItExpires(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, true)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "two renewals", func() bool { return fake.count(fakeRenewSelfPath) >= wantRenewals })
	if lookups := fake.count(fakeLookupSelfPath); lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestTransitKeepsRetryingAFailedRenewal(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, true)
	fake.setRenewFails(true)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "failed renewals", func() bool { return fake.count(fakeRenewSelfPath) >= wantFailedRenewal })
	fake.setRenewFails(false)
	failed := fake.count(fakeRenewSelfPath)
	waitFor(t, "a renewal after recovery", func() bool {
		return fake.count(fakeRenewSelfPath) >= failed+wantRenewals
	})
	if _, err := transit.Wrap(context.Background(), testDEK(t), DEKAAD(environmentID)); err != nil {
		t.Fatalf("wrap after recovery: %v", err)
	}
}

func TestTransitExpiredTokenFailsEveryCall(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, false)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	ctx := context.Background()
	aad := DEKAAD(environmentID)
	wrapped, err := transit.Wrap(ctx, testDEK(t), aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	var leap atomic.Int64
	transit.now = func() time.Time { return time.Now().Add(time.Duration(leap.Load())) }
	startRenewal(t, transit)
	waitFor(t, "the token lookup", func() bool { return fake.count(fakeLookupSelfPath) == 1 })
	waitFor(t, "the token expiry", func() bool { return transit.tokenExpiry() != time.Time{} })

	leap.Store(int64(testClockLeap))

	if _, err := transit.Wrap(ctx, testDEK(t), aad); !errors.Is(err, ErrTransitTokenExpired) {
		t.Fatalf("wrap: err = %v, want ErrTransitTokenExpired", err)
	}
	if _, err := transit.Unwrap(ctx, wrapped, aad); !errors.Is(err, ErrTransitTokenExpired) {
		t.Fatalf("unwrap: err = %v, want ErrTransitTokenExpired", err)
	}
	if _, err := transit.KeyID(ctx); !errors.Is(err, ErrTransitTokenExpired) {
		t.Fatalf("key id: err = %v, want ErrTransitTokenExpired", err)
	}
	if renewals := fake.count(fakeRenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want none for a token that is not renewable", renewals)
	}
}

func TestTransitDoesNotRenewATokenWithoutTTL(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(0, false)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "the token lookup", func() bool { return fake.count(fakeLookupSelfPath) == 1 })
	time.Sleep(testQuietPeriod)
	if renewals := fake.count(fakeRenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want none", renewals)
	}
	if expiry := transit.tokenExpiry(); !expiry.IsZero() {
		t.Fatalf("token expiry = %v, want none", expiry)
	}
}

func TestTransitRetriesAFailedLookup(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, true)
	fake.token = "s.other"
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "lookup retries", func() bool { return fake.count(fakeLookupSelfPath) >= wantFailedRenewal })
	if renewals := fake.count(fakeRenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want none before a lookup succeeds", renewals)
	}
}

func TestTransitRenewalDelay(t *testing.T) {
	transit := &Transit{renewMargin: time.Minute}
	if delay := transit.renewalDelay(time.Hour); delay != time.Hour-time.Minute {
		t.Fatalf("delay for an hour-long token = %v, want %v", delay, time.Hour-time.Minute)
	}
	if delay := transit.renewalDelay(time.Minute); delay != time.Minute/shortTokenRenewDivisor {
		t.Fatalf("delay for a token as long as the margin = %v, want half its ttl", delay)
	}
}

func TestTransitRenewalWithoutALeaseKeepsTheExpiry(t *testing.T) {
	fake := newFakeTransit(t)
	fake.setToken(testTokenTTL, true)
	server := fake.server()
	transit := newTestTransit(t, transitConfig(server.URL))
	startRenewal(t, transit)
	waitFor(t, "the token expiry", func() bool { return !transit.tokenExpiry().IsZero() })

	fake.setToken(0, true)
	renewals := fake.count(fakeRenewSelfPath)
	waitFor(t, "renewals without a lease", func() bool {
		return fake.count(fakeRenewSelfPath) >= renewals+wantFailedRenewal
	})
	if expiry := transit.tokenExpiry(); expiry.IsZero() {
		t.Fatal("token expiry was cleared by a renewal without a lease")
	}
}
