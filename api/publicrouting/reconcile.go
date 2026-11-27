// Package publicrouting maintains provider-neutral intent and drains placement
// retirements. Provider calls belong to separately deployed publishers.
package publicrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/team-loco/loco/api/gen/db"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

type Config struct {
	Interval     time.Duration
	RetrySeconds int32
	BatchSize    int32
}

type Reconciler struct {
	pool   *pgxpool.Pool
	config Config
}

func New(pool *pgxpool.Pool, config Config) *Reconciler {
	return &Reconciler{pool: pool, config: config}
}

func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()
	for {
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "public routing reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) RunOnce(ctx context.Context) error {
	q := db.New(r.pool)
	bindings, err := q.ListUnbootstrappedPublishers(ctx)
	if err != nil {
		return fmt.Errorf("list publisher imports: %w", err)
	}
	for _, binding := range bindings {
		if importErr := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
			qtx := db.New(tx)
			if importErr := qtx.EnqueueBoundRouteAssignments(ctx, &binding.AppDomain); importErr != nil {
				return importErr
			}
			return qtx.MarkPublisherBootstrapped(ctx, binding.ID)
		}); importErr != nil {
			return fmt.Errorf("import publisher assignments: %w", importErr)
		}
	}
	work, err := q.ListPublicRoutingWork(ctx, r.config.BatchSize)
	if err != nil {
		return fmt.Errorf("list public routing work: %w", err)
	}
	var failures []error
	for _, key := range work {
		err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
			qtx := db.New(tx)
			// Resource mutations acquire this same row lock before their placement/domain changes.
			if _, err := qtx.LockResources(ctx, []uuid.UUID{key.ResourceID}); err != nil {
				return err
			}
			if err := qtx.LockPublicRoutingWork(
				ctx,
				db.LockPublicRoutingWorkParams(key),
			); err != nil {
				return err
			}
			return r.reconcile(ctx, qtx, key)
		})
		if err != nil {
			failures = append(
				failures,
				fmt.Errorf("resource %s environment %s: %w", key.ResourceID, key.EnvironmentID, err),
			)
			// Failed resources must not starve later work in a bounded batch.
			if retryErr := q.ReschedulePublicRoutingWork(
				ctx,
				db.ReschedulePublicRoutingWorkParams{
					ResourceID:    key.ResourceID,
					EnvironmentID: key.EnvironmentID,
					RetrySeconds:  r.config.RetrySeconds,
				},
			); retryErr != nil {
				failures = append(failures, retryErr)
			}
		}
	}
	return errors.Join(failures...)
}

func (r *Reconciler) reconcile(ctx context.Context, q *db.Queries, key db.ListPublicRoutingWorkRow) error {
	publications, err := q.ListResourceRoutePublications(
		ctx,
		db.ListResourceRoutePublicationsParams(key),
	)
	if err != nil {
		return err
	}
	domains, err := q.ListBoundResourceDomains(
		ctx,
		db.ListBoundResourceDomainsParams(key),
	)
	if err != nil {
		return err
	}
	var publishers []uuid.UUID
	for _, p := range publications {
		publishers = append(publishers, p.PublisherID)
	}
	for _, d := range domains {
		publishers = append(publishers, d.PublisherID)
	}
	if _, err = q.LockRoutePublishers(ctx, publishers); err != nil {
		return err
	}
	placements, err := q.ListRoutingPlacements(
		ctx,
		db.ListRoutingPlacementsParams(key),
	)
	if err != nil {
		return err
	}
	hasTargets, err := q.HasDesiredRoutingTargets(ctx, db.HasDesiredRoutingTargetsParams(key))
	if err != nil {
		return err
	}
	assigned := make(map[string]uuid.UUID, len(domains))
	for _, d := range domains {
		assigned[d.Domain] = d.PublisherID
	}
	existing := make(map[string]db.PublicRoutePublication, len(publications))
	for _, p := range publications {
		existing[p.Hostname] = p
		if _, ok := assigned[p.Hostname]; !ok {
			if err = saveIntent(ctx, q, p, []uuid.UUID{}, true); err != nil {
				return err
			}
		}
	}
	for _, domain := range domains {
		publication, exists := existing[domain.Domain]
		if !exists {
			publication = db.PublicRoutePublication{
				PublisherID:   domain.PublisherID,
				Hostname:      domain.Domain,
				ResourceID:    key.ResourceID,
				EnvironmentID: key.EnvironmentID,
			}
		}
		clusters, selectionErr := eligibleClusters(placements, publication)
		if selectionErr != nil {
			return selectionErr
		}
		// Keep the established route while replacement placements are still starting.
		if hasTargets && len(clusters) == 0 {
			for _, placement := range placements {
				if slices.Contains(publication.ClusterIds, placement.ClusterID) {
					clusters = append(clusters, placement.ClusterID)
				}
			}
		}
		slices.SortFunc(clusters, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		if err = saveIntent(ctx, q, publication, clusters, !hasTargets); err != nil {
			return err
		}
	}
	if err = finalizeRetirements(ctx, q, key); err != nil {
		return err
	}
	if err = refreshHostnames(ctx, q, key, placements); err != nil {
		return err
	}
	publications, err = q.ListResourceRoutePublications(
		ctx,
		db.ListResourceRoutePublicationsParams(key),
	)
	if err != nil {
		return err
	}
	pending := false
	for _, p := range publications {
		acknowledged := p.AcknowledgedRevision == p.Revision
		drained := p.DrainUntil != nil && !p.DrainUntil.After(time.Now())
		if !acknowledged || !drained {
			pending = true
			break
		}
	}
	if pending {
		return q.ReschedulePublicRoutingWork(
			ctx,
			db.ReschedulePublicRoutingWorkParams{
				ResourceID:    key.ResourceID,
				EnvironmentID: key.EnvironmentID,
				RetrySeconds:  r.config.RetrySeconds,
			},
		)
	}
	return q.DeletePublicRoutingDirty(
		ctx,
		db.DeletePublicRoutingDirtyParams(key),
	)
}

func eligibleClusters(
	placements []db.ListRoutingPlacementsRow,
	publication db.PublicRoutePublication,
) ([]uuid.UUID, error) {
	clusters := make([]uuid.UUID, 0, len(placements))
	for _, p := range placements {
		if p.Retiring {
			continue
		}
		if slices.Contains(publication.ClusterIds, p.ClusterID) {
			clusters = append(clusters, p.ClusterID)
			continue
		}
		if p.ReadyReplicas == 0 || len(p.PublicRoute) == 0 {
			continue
		}
		var route loco.PublicRouteStatus
		if err := json.Unmarshal(p.PublicRoute, &route); err != nil {
			return nil, fmt.Errorf("decode placement route %s: %w", p.ID, err)
		}
		currentAccepted := route.Ready && route.Revision == p.DesiredRevision
		if !currentAccepted || !slices.Contains(route.Hostnames, publication.Hostname) {
			continue
		}
		var payload loco.PlacementPayload
		if err := json.Unmarshal(p.DesiredSpec, &payload); err != nil {
			return nil, fmt.Errorf("decode placement %s: %w", p.ID, err)
		}
		if payload.AppSpec == nil || payload.AppSpec.ServiceSpec == nil {
			return nil, fmt.Errorf("placement %s has no service spec", p.ID)
		}
		routing := payload.AppSpec.ServiceSpec.Routing
		if routing != nil && slices.Equal(routing.Hostnames, route.Hostnames) {
			clusters = append(clusters, p.ClusterID)
		}
	}
	slices.SortFunc(clusters, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return slices.Compact(clusters), nil
}

func saveIntent(
	ctx context.Context,
	q *db.Queries,
	p db.PublicRoutePublication,
	clusters []uuid.UUID,
	withdrawing bool,
) error {
	sameMembership := p.Withdrawing == withdrawing && slices.Equal(p.ClusterIds, clusters)
	if p.ID != uuid.Nil && sameMembership {
		return nil
	}
	if clusters == nil {
		clusters = []uuid.UUID{}
	}
	sequence, err := q.NextRouteChangeSequence(ctx, p.PublisherID)
	if err != nil {
		return err
	}
	_, err = q.UpsertRouteIntent(
		ctx,
		db.UpsertRouteIntentParams{
			PublisherID:    p.PublisherID,
			Hostname:       p.Hostname,
			ResourceID:     p.ResourceID,
			EnvironmentID:  p.EnvironmentID,
			ClusterIds:     clusters,
			Withdrawing:    withdrawing,
			ChangeSequence: sequence,
		},
	)
	return err
}

func finalizeRetirements(ctx context.Context, q *db.Queries, key db.ListPublicRoutingWorkRow) error {
	retirements, err := q.ListReadyRoutingRetirements(
		ctx,
		db.ListReadyRoutingRetirementsParams(key),
	)
	if err != nil {
		return err
	}
	for _, retirement := range retirements {
		rows, err := q.MarkPlacementDeleted(
			ctx,
			db.MarkPlacementDeletedParams{
				ResourceID:    key.ResourceID,
				EnvironmentID: key.EnvironmentID,
				ClusterID:     retirement.ClusterID,
			},
		)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.ClosePlacementWindow(
				ctx,
				db.ClosePlacementWindowParams{ResourceID: key.ResourceID, ClusterID: row.ClusterID},
			); err != nil {
				return err
			}
			if err := q.NotifyClusterPlacements(ctx, row.ClusterID.String()); err != nil {
				return err
			}
		}
		if err := q.DeleteRoutingRetirement(
			ctx,
			db.DeleteRoutingRetirementParams{
				ResourceID:    key.ResourceID,
				EnvironmentID: key.EnvironmentID,
				ClusterID:     retirement.ClusterID,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func refreshHostnames(
	ctx context.Context,
	q *db.Queries,
	key db.ListPublicRoutingWorkRow,
	placements []db.ListRoutingPlacementsRow,
) error {
	rows, err := q.EffectiveRoutingHostnames(
		ctx,
		db.EffectiveRoutingHostnamesParams(key),
	)
	if err != nil {
		return err
	}
	hostnames := make([]string, 0, len(rows))
	for _, row := range rows {
		hostnames = append(hostnames, row.Domain)
	}
	for _, p := range placements {
		if p.Retiring {
			continue
		}
		var payload loco.PlacementPayload
		if err := json.Unmarshal(p.DesiredSpec, &payload); err != nil {
			return err
		}
		if payload.AppSpec == nil || payload.AppSpec.ServiceSpec == nil {
			return fmt.Errorf("placement %s has no service spec", p.ID)
		}
		routing := payload.AppSpec.ServiceSpec.Routing
		if routing == nil {
			continue
		}
		if slices.Equal(routing.Hostnames, hostnames) {
			continue
		}
		routing.Hostnames = hostnames
		if len(hostnames) == 0 {
			payload.AppSpec.ServiceSpec.Routing = nil
		}
		desired, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := q.UpsertPlacement(
			ctx,
			db.UpsertPlacementParams{
				ResourceID:    p.ResourceID,
				EnvironmentID: p.EnvironmentID,
				ClusterID:     p.ClusterID,
				Region:        p.Region,
				DeploymentID:  p.DeploymentID,
				DesiredSpec:   desired,
				SecretEnv:     p.SecretEnv,
			},
		); err != nil {
			return err
		}
		if err := q.NotifyClusterPlacements(ctx, p.ClusterID.String()); err != nil {
			return err
		}
	}
	return nil
}
