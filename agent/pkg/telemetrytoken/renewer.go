package telemetrytoken

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/team-loco/loco/agent/pkg/rpcretry"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
)

const renewalDivisor = 2

var errNamespaceGone = errors.New("namespace does not exist")

type Secret struct {
	Namespaces []string
	Name       string
	Key        string
}

type Renewer struct {
	client  ingestv1connect.IngestServiceClient
	kube    kubernetes.Interface
	secret  Secret
	backoff func() wait.Backoff
}

func NewRenewer(
	client ingestv1connect.IngestServiceClient,
	kube kubernetes.Interface,
	secret Secret,
	backoff func() wait.Backoff,
) *Renewer {
	return &Renewer{client: client, kube: kube, secret: secret, backoff: backoff}
}

func RenewalDelay(now, expiresAt time.Time) time.Duration {
	return expiresAt.Sub(now) / renewalDivisor
}

// Run renews the token until ctx ends or a renewal fails with an error a retry cannot fix.
func (r *Renewer) Run(ctx context.Context) error {
	backoff := r.backoff()
	for {
		expiresAt, err := r.renew(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !retryable(err) {
			return fmt.Errorf("telemetry token renewal: %w", err)
		}
		var delay time.Duration
		if err != nil {
			delay = backoff.Step()
			slog.WarnContext(ctx, "telemetry token renewal failed", "error", err, "retry_in", delay)
		} else {
			backoff = r.backoff()
			delay = RenewalDelay(time.Now(), expiresAt)
			slog.InfoContext(ctx, "telemetry token renewed", "expires_at", expiresAt, "next_renewal_in", delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func retryable(err error) bool {
	return rpcretry.Retryable(err) && !apierrors.IsForbidden(err) && !errors.Is(err, errNamespaceGone)
}

func (r *Renewer) renew(ctx context.Context) (time.Time, error) {
	resp, err := r.client.IssueToken(ctx, &ingestv1.IssueTokenRequest{})
	if err != nil {
		return time.Time{}, fmt.Errorf("issue telemetry token: %w", err)
	}
	for _, namespace := range r.secret.Namespaces {
		if err := r.write(ctx, r.kube.CoreV1().Secrets(namespace), resp.GetToken()); err != nil {
			return time.Time{}, fmt.Errorf("telemetry token Secret %s/%s: %w", namespace, r.secret.Name, err)
		}
	}
	return resp.GetExpiresAt().AsTime(), nil
}

func (r *Renewer) write(ctx context.Context, secrets corev1client.SecretInterface, token string) error {
	secret, err := secrets.Get(ctx, r.secret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created := &corev1.Secret{
			Name: r.secret.Name,
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{r.secret.Key: []byte(token)},
		}
		_, createErr := secrets.Create(ctx, created, metav1.CreateOptions{})
		if apierrors.IsNotFound(createErr) {
			return fmt.Errorf("create: %w: %w", errNamespaceGone, createErr)
		}
		if createErr != nil {
			return fmt.Errorf("create: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[r.secret.Key] = []byte(token)
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
