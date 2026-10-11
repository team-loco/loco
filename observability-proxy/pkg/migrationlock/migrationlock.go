package migrationlock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

var (
	ErrInvalidName      = errors.New("lease name is not a DNS subdomain")
	ErrInvalidNamespace = errors.New("lease namespace is not a DNS label")
	ErrMissingIdentity  = errors.New("holder identity is required")
	ErrNotPositive      = errors.New("lease timings must be positive")
	ErrRenewDeadline    = errors.New("renew deadline must be shorter than the lease duration")
	ErrRetryPeriod      = errors.New("retry period with jitter must be shorter than the renew deadline")
	ErrNotAcquired      = errors.New("migration lease was not acquired")
	ErrLeaseLost        = errors.New("migration lease was lost before the migration finished")
)

type Config struct {
	Name          string
	Namespace     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

func (c Config) Validate() error {
	if problems := validation.IsDNS1123Subdomain(c.Name); len(problems) > 0 {
		detail := strings.Join(problems, "; ")
		return fmt.Errorf("%w: %q: %s", ErrInvalidName, c.Name, detail)
	}
	if problems := validation.IsDNS1123Label(c.Namespace); len(problems) > 0 {
		detail := strings.Join(problems, "; ")
		return fmt.Errorf("%w: %q: %s", ErrInvalidNamespace, c.Namespace, detail)
	}
	if c.Identity == "" {
		return ErrMissingIdentity
	}
	if c.LeaseDuration <= 0 || c.RenewDeadline <= 0 || c.RetryPeriod <= 0 {
		return fmt.Errorf("%w: duration %s, renew deadline %s, retry period %s",
			ErrNotPositive, c.LeaseDuration, c.RenewDeadline, c.RetryPeriod)
	}
	if c.RenewDeadline >= c.LeaseDuration {
		return fmt.Errorf(
			"%w: renew deadline %s, lease duration %s",
			ErrRenewDeadline,
			c.RenewDeadline,
			c.LeaseDuration,
		)
	}
	if jittered := time.Duration(leaderelection.JitterFactor * float64(c.RetryPeriod)); jittered >= c.RenewDeadline {
		return fmt.Errorf("%w: retry period %s, renew deadline %s", ErrRetryPeriod, c.RetryPeriod, c.RenewDeadline)
	}
	return nil
}

type Lease struct {
	client coordinationv1client.LeasesGetter
	cfg    Config
}

func New(client coordinationv1client.LeasesGetter, cfg Config) *Lease {
	return &Lease{client: client, cfg: cfg}
}

func (l *Lease) logAttrs() []any {
	return []any{"lease", l.cfg.Name, "namespace", l.cfg.Namespace, "holder", l.cfg.Identity}
}

type heldLease struct {
	*resourcelock.LeaseLock
	held     atomic.Bool
	released atomic.Bool
}

func (h *heldLease) Create(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	if err := h.LeaseLock.Create(ctx, record); err != nil {
		return err
	}
	if record.HolderIdentity == h.Identity() {
		h.held.Store(true)
	}
	return nil
}

func (h *heldLease) Update(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	if err := h.LeaseLock.Update(ctx, record); err != nil {
		return err
	}
	if record.HolderIdentity == h.Identity() {
		h.held.Store(true)
	}
	if record.HolderIdentity == "" && h.held.Load() {
		h.released.Store(true)
	}
	return nil
}

func (l *Lease) WithLock(ctx context.Context, fn func(context.Context) error) error {
	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	lock := &heldLease{LeaseLock: &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: l.cfg.Name, Namespace: l.cfg.Namespace},
		Client:     l.client,
		LockConfig: resourcelock.ResourceLockConfig{Identity: l.cfg.Identity},
	}}
	var result error
	done := make(chan struct{})
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   l.cfg.LeaseDuration,
		RenewDeadline:   l.cfg.RenewDeadline,
		RetryPeriod:     l.cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            l.cfg.Name,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadCtx context.Context) {
				defer close(done)
				slog.InfoContext(ctx, "acquired migration lease", l.logAttrs()...)
				result = fn(leadCtx)
				if leadCtx.Err() != nil && runCtx.Err() == nil {
					result = errors.Join(ErrLeaseLost, result)
				}
				stop()
			},
			OnStoppedLeading: func() {
				if lock.released.Load() {
					slog.InfoContext(ctx, "released migration lease", l.logAttrs()...)
					return
				}
				if lock.held.Load() {
					slog.WarnContext(
						ctx,
						"stopped holding migration lease without releasing it",
						append(l.logAttrs(), "expires_after", l.cfg.LeaseDuration)...)
				}
			},
			OnNewLeader: func(identity string) {
				if identity != "" && identity != l.cfg.Identity {
					slog.InfoContext(
						ctx,
						"waiting for migration lease",
						append(l.logAttrs(), "current_holder", identity)...)
				}
			},
		},
	})
	if err != nil {
		return fmt.Errorf("configure migration lease: %w", err)
	}

	slog.InfoContext(ctx, "acquiring migration lease", l.logAttrs()...)
	elector.Run(runCtx)
	if !lock.held.Load() {
		cause := context.Cause(ctx)
		return fmt.Errorf("%w: %s/%s: %w", ErrNotAcquired, l.cfg.Namespace, l.cfg.Name, cause)
	}
	<-done
	return result
}

type None struct{}

func (None) WithLock(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
