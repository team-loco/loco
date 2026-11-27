package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	routingv1 "github.com/team-loco/loco/gen/go/loco/routing/v1"
	"github.com/team-loco/loco/internal/buildinfo"
	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/rpc"
)

var version string

type publication struct {
	intent      *routingv1.RouteIntent
	nextAttempt time.Time
}
type publisher struct {
	binding      binding
	core         *rpc.Clients
	dns          *dnsClient
	session      string
	cursor       int64
	publications map[string]*publication
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.Version(version))
		return
	}
	path := envconfig.String("PUBLISHER_CONFIG_FILE", "")
	if path == "" {
		panic("PUBLISHER_CONFIG_FILE is required")
	}
	c, err := readConfig(path)
	if err != nil {
		panic(err)
	}
	publishers, err := newPublishers(c)
	if err != nil {
		panic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(time.Duration(c.PollSeconds) * time.Second)
	defer ticker.Stop()
	var blockedUntil time.Time
	for {
		if !time.Now().Before(blockedUntil) {
			budget := c.BatchSize
			for _, p := range publishers {
				retryAt, runErr := p.runOnce(ctx, c, &budget)
				if runErr != nil {
					slog.WarnContext(
						ctx,
						"public routing publication retry",
						"app_domain",
						p.binding.AppDomain,
						"error",
						runErr,
					)
				}
				if retryAt.After(blockedUntil) {
					blockedUntil = retryAt
					break
				}
			}
		}
		first := publishers[0]
		copy(publishers, publishers[1:])
		publishers[len(publishers)-1] = first
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func newPublishers(c config) ([]*publisher, error) {
	publishers := make([]*publisher, 0, len(c.Bindings))
	for _, b := range c.Bindings {
		locoToken, err := readToken(b.LocoTokenFile)
		if err != nil {
			return nil, err
		}
		providerToken, err := readToken(b.CloudflareTokenFile)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: time.Duration(c.RequestSeconds) * time.Second}
		publishers = append(
			publishers,
			&publisher{
				binding: b,
				core:    rpc.NewHTTPClients(client, c.APIURL, locoToken),
				dns: &dnsClient{
					client:  client,
					baseURL: cloudflareAPI,
					token:   providerToken,
					zoneID:  b.ZoneID,
				},
				session:      uuid.NewString(),
				publications: map[string]*publication{},
			},
		)
	}
	return publishers, nil
}
func (p *publisher) refresh(ctx context.Context) (time.Time, error) {
	var deadline time.Time
	for {
		started := time.Now()
		page, err := p.core.Routing.ListRouteIntents(
			ctx,
			&routingv1.ListRouteIntentsRequest{
				PublisherId:   p.binding.PublisherID,
				SessionId:     p.session,
				AfterSequence: p.cursor,
			},
		)
		if err != nil {
			if connect.CodeOf(err) == connect.CodeInvalidArgument && p.cursor != 0 {
				p.cursor = 0
				p.publications = map[string]*publication{}
				continue
			}
			return deadline, fmt.Errorf("read routing intent: %w", err)
		}
		if page.GetSessionSeconds() <= 0 {
			return deadline, errors.New("routing API returned an invalid writer lease")
		}
		deadline = started.Add(time.Duration(page.GetSessionSeconds()) * time.Second)
		for _, intent := range page.GetIntents() {
			old := p.publications[intent.GetPublicationId()]
			if old == nil || old.intent.GetRevision() != intent.GetRevision() {
				p.publications[intent.GetPublicationId()] = &publication{intent: intent}
			}
		}
		p.cursor = page.GetNextSequence()
		if !page.GetHasMore() {
			return deadline, nil
		}
	}
}
func (p *publisher) runOnce(ctx context.Context, c config, budget *int) (time.Time, error) {
	if _, err := p.refresh(ctx); err != nil {
		return time.Time{}, err
	}
	keys := make([]string, 0, len(p.publications))
	for key := range p.publications {
		keys = append(keys, key)
	}
	// Oldest attempts first prevent a broken hostname from starving later work.
	slices.SortFunc(keys, func(a, b string) int {
		pa, pb := p.publications[a], p.publications[b]
		if order := pa.nextAttempt.Compare(pb.nextAttempt); order != 0 {
			return order
		}
		return strings.Compare(a, b)
	})
	for _, key := range keys {
		if *budget == 0 {
			return time.Time{}, nil
		}
		item := p.publications[key]
		if item == nil {
			continue
		}
		if time.Now().Before(item.nextAttempt) {
			continue
		}
		if !item.intent.GetWithdrawing() && len(item.intent.GetClusterIds()) == 0 {
			continue
		}
		deadline, err := p.refresh(ctx)
		if err != nil {
			return time.Time{}, err
		}
		item = p.publications[key]
		// Resnapshot during renewal can remove a key captured above.
		if item == nil {
			continue
		}
		intent := item.intent
		if !intent.GetWithdrawing() && len(intent.GetClusterIds()) == 0 {
			continue
		}
		// Both DNS mutation and acknowledgement must finish inside the current writer lease.
		leaseCtx, cancel := context.WithDeadline(ctx, deadline)
		err = p.publish(leaseCtx, intent)
		cancel()
		*budget--
		// A failed item remains eligible on the next poll while other items can proceed now.
		item.nextAttempt = time.Now().Add(time.Duration(c.AuditSeconds) * time.Second)
		if err != nil {
			item.nextAttempt = time.Now().Add(time.Duration(c.PollSeconds) * time.Second)
			var provider *providerError
			if errors.As(err, &provider) && provider.status == http.StatusTooManyRequests {
				retryAt := provider.retryAt
				if retryAt.IsZero() {
					retryAt = time.Now().Add(time.Duration(c.PollSeconds) * time.Second)
				}
				return retryAt, err
			}
			slog.WarnContext(ctx, "hostname publication retry", "hostname", intent.GetHostname(), "error", err)
		}
	}
	return time.Time{}, nil
}
func (p *publisher) publish(ctx context.Context, intent *routingv1.RouteIntent) error {
	if !strings.HasSuffix(intent.GetHostname(), "."+p.binding.AppDomain) {
		return errors.New("routing intent is outside the configured app domain")
	}
	if intent.GetPreference() != "nearest" {
		return errors.New("unsupported routing preference")
	}
	target := ""
	var err error
	if !intent.GetWithdrawing() {
		target, err = p.binding.target(intent.GetClusterIds())
		if err == nil && target == intent.GetHostname() {
			err = errors.New("shared destination would create a CNAME loop")
		}
	}
	if err == nil {
		err = p.dns.reconcile(ctx, intent.GetHostname(), target, intent.GetPublicationId(), intent.GetWithdrawing())
	}
	outcome := routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED
	message := ""
	if err != nil {
		outcome = routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR
		message = err.Error()
	}
	_, reportErr := p.core.Routing.ReportPublication(
		ctx,
		&routingv1.ReportPublicationRequest{
			PublisherId:   p.binding.PublisherID,
			SessionId:     p.session,
			PublicationId: intent.GetPublicationId(),
			Revision:      intent.GetRevision(),
			Outcome:       outcome,
			Message:       message,
		},
	)
	if reportErr != nil {
		return errors.Join(err, fmt.Errorf("acknowledge routing publication: %w", reportErr))
	}
	return err
}
