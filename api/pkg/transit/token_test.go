package transit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
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

func startRenewal(t *testing.T, transit *Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	lease, err := transit.logIn(ctx)
	if err != nil {
		cancel()
		t.Fatalf("log in: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		transit.renewToken(ctx, lease)
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
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, true)
	server := fake.Server()
	transit := newTestClient(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "two renewals", func() bool { return fake.Count(transittest.RenewSelfPath) >= wantRenewals })
	if lookups := fake.Count(transittest.LookupSelfPath); lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestTransitKeepsRetryingAFailedRenewal(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, true)
	fake.SetRenewFails(true)
	server := fake.Server()
	transit := newTestClient(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "failed renewals", func() bool { return fake.Count(transittest.RenewSelfPath) >= wantFailedRenewal })
	fake.SetRenewFails(false)
	failed := fake.Count(transittest.RenewSelfPath)
	waitFor(t, "a renewal after recovery", func() bool {
		return fake.Count(transittest.RenewSelfPath) >= failed+wantRenewals
	})
	if _, _, err := testKey(transit).Encrypt(context.Background(), testPlaintext(t), testContext); err != nil {
		t.Fatalf("encrypt after recovery: %v", err)
	}
}

func TestTransitExpiredTokenFailsEveryCall(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, false)
	server := fake.Server()
	transit := newTestClient(t, transitConfig(server.URL))
	ctx := context.Background()
	keyID, ciphertext, err := testKey(transit).Encrypt(ctx, testPlaintext(t), testContext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	var leap atomic.Int64
	transit.now = func() time.Time { return time.Now().Add(time.Duration(leap.Load())) }
	startRenewal(t, transit)

	leap.Store(int64(testClockLeap))

	if _, _, err := testKey(transit).Encrypt(ctx, testPlaintext(t), testContext); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("encrypt: err = %v, want ErrTokenExpired", err)
	}
	if _, err := testKey(transit).Decrypt(ctx, keyID, ciphertext, testContext); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("decrypt: err = %v, want ErrTokenExpired", err)
	}
	if _, err := testKey(transit).KeyID(ctx); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("key id: err = %v, want ErrTokenExpired", err)
	}
	if renewals := fake.Count(transittest.RenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want none for a token that is not renewable", renewals)
	}
}

func TestTransitDoesNotRenewATokenWithoutTTL(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(0, false)
	server := fake.Server()
	transit := newTestClient(t, transitConfig(server.URL))
	startRenewal(t, transit)

	time.Sleep(testQuietPeriod)
	if renewals := fake.Count(transittest.RenewSelfPath); renewals != 0 {
		t.Fatalf("renewals = %d, want none", renewals)
	}
	if expiry := transit.tokenExpiry(); !expiry.IsZero() {
		t.Fatalf("token expiry = %v, want none", expiry)
	}
}

func TestTransitRenewalDelay(t *testing.T) {
	transit := &Client{renewMargin: time.Minute}
	if delay := transit.renewalDelay(time.Hour); delay != time.Hour-time.Minute {
		t.Fatalf("delay for an hour-long token = %v, want %v", delay, time.Hour-time.Minute)
	}
	if delay := transit.renewalDelay(time.Minute); delay != time.Minute/shortTokenRenewDivisor {
		t.Fatalf("delay for a token as long as the margin = %v, want half its ttl", delay)
	}
}

func TestTransitRenewalWithoutALeaseKeepsTheExpiry(t *testing.T) {
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, true)
	server := fake.Server()
	transit := newTestClient(t, transitConfig(server.URL))
	startRenewal(t, transit)

	fake.SetToken(0, true)
	renewals := fake.Count(transittest.RenewSelfPath)
	waitFor(t, "renewals without a lease", func() bool {
		return fake.Count(transittest.RenewSelfPath) >= renewals+wantFailedRenewal
	})
	if expiry := transit.tokenExpiry(); expiry.IsZero() {
		t.Fatal("token expiry was cleared by a renewal without a lease")
	}
}

func TestTransitTokenRefreshRecordsARootSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	fake := transittest.New(t)
	fake.SetToken(testTokenTTL, true)
	server := fake.Server()
	transit := newTestClient(t, transitConfig(server.URL))
	startRenewal(t, transit)

	waitFor(t, "a traced token refresh with its request", func() bool {
		for _, refresh := range recorder.Ended() {
			if refresh.Name() != tokenRefreshSpan || refresh.Parent().IsValid() {
				continue
			}
			for _, request := range recorder.Ended() {
				if request.Parent().SpanID() == refresh.SpanContext().SpanID() {
					return true
				}
			}
		}
		return false
	})
}
