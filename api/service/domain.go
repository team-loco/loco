package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/tvm"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrDomainAlreadyExists     = errors.New("domain already exists")
	ErrCannotRemovePrimary     = errors.New("make another domain primary before removing the primary domain")
	errNoProductionEnvironment = errors.New("the workspace has no production environment to hold the domain")
)

type DomainServer struct {
	db          *pgxpool.Pool
	queries     genDb.Querier
	authz       *authz.Authorizer
	machine     *tvm.VendingMachine
	defaults    servicedefaults.Defaults
	silentAfter time.Duration
}

func NewDomainServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	machine *tvm.VendingMachine,
	defaults servicedefaults.Defaults,
	clusterSilentAfter time.Duration,
) *DomainServer {
	return &DomainServer{
		db:          db,
		queries:     queries,
		machine:     machine,
		authz:       authz.New(db, queries),
		defaults:    defaults,
		silentAfter: clusterSilentAfter,
	}
}

// ListPlatformDomains lists the platform domains the clusters an environment deploys to serve.
func (s *DomainServer) ListPlatformDomains(
	ctx context.Context,
	req *domainv1.ListPlatformDomainsRequest,
) (*domainv1.ListPlatformDomainsResponse, error) {
	environmentID := uuid.MustParse(req.GetEnvironmentId())
	env, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", environmentID)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}
	workspaceAction := actions.New(actions.ListPlatformDomains, env.WorkspaceID.String())
	if authErr := s.authz.Check(ctx, scopes, workspaceAction); authErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(authErr)
	}
	eligible, err := eligibleClusters(ctx, s.queries, env.EnvironmentType, s.silentAfter)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list clusters", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	clusters := regionClusters(eligible)
	served := configplan.ServedDomains(clusters)
	domains := make([]*domainv1.PlatformDomain, 0, len(served))
	for _, domain := range served {
		platformDomain := &domainv1.PlatformDomain{Domain: domain}
		for _, cluster := range clusters {
			if slices.Contains(cluster.Domains, domain) {
				platformDomain.Regions = append(platformDomain.Regions, cluster.Region)
			}
		}
		domains = append(domains, platformDomain)
	}
	return &domainv1.ListPlatformDomainsResponse{PlatformDomains: domains}, nil
}

// ListLocoOwnedDomains lists all loco-owned (subdomain) domains (admin only)
func (s *DomainServer) ListLocoOwnedDomains(
	ctx context.Context,
	_ *domainv1.ListLocoOwnedDomainsRequest,
) (*domainv1.ListLocoOwnedDomainsResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.NewSystem(actions.ListLocoOwnedDomains),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list loco owned domains")
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	results, err := s.queries.ListAllLocoOwnedDomains(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list loco owned domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to list loco owned domains")
	}

	domains := make([]*domainv1.LocoOwnedDomain, len(results))
	for i, result := range results {
		domains[i] = &domainv1.LocoOwnedDomain{
			Id:             result.ID.String(),
			Domain:         result.Domain,
			ResourceName:   result.ResourceName,
			ResourceId:     result.ResourceID.String(),
			PlatformDomain: result.PlatformDomain,
		}
	}

	return &domainv1.ListLocoOwnedDomainsResponse{
		Domains: domains,
	}, nil
}

// CreateResourceDomain adds a new domain to a resource
func (s *DomainServer) CreateResourceDomain(
	ctx context.Context,
	req *domainv1.CreateResourceDomainRequest,
) (*domainv1.CreateResourceDomainResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.AddDomain, req.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	resourceID := uuid.MustParse(req.GetResourceId())
	fullDomain := req.GetDomain().GetSubdomain() + "." + req.GetDomain().GetPlatformDomain()
	platformDomain, subdomain, err := s.checkResourceDomain(ctx, resourceID, fullDomain)
	if err != nil {
		return nil, err
	}

	// check domain availability
	available, err := s.queries.CheckDomainAvailability(ctx, fullDomain)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if !available {
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
	}

	var resourceDomain uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceDomains(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		environmentID, envErr := imperativeDomainEnvironment(ctx, qtx, resourceID)
		if envErr != nil {
			return envErr
		}
		hasPrimary, primaryErr := qtx.ResourceHasPrimaryDomain(ctx, genDb.ResourceHasPrimaryDomainParams{
			ResourceID:    resourceID,
			EnvironmentID: environmentID,
		})
		if primaryErr != nil {
			return fmt.Errorf("check primary domain: %w", primaryErr)
		}
		created, createErr := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
			ResourceID:     resourceID,
			EnvironmentID:  environmentID,
			Domain:         fullDomain,
			DomainSource:   genDb.DomainSourcePlatformProvided,
			SubdomainLabel: &subdomain,
			PlatformDomain: &platformDomain,
			IsPrimary:      !hasPrimary,
		})
		if createErr != nil {
			return createErr
		}
		resourceDomain = created
		if routeErr := s.refreshDomainRouting(ctx, qtx, resourceID, environmentID); routeErr != nil {
			return routeErr
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DomainCreated,
			ResourceID:  new(resourceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(resourceDomain),
			Data:        map[string]any{events.FieldDomain: fullDomain, events.FieldResourceID: resourceID.String()},
		})
	})
	if errors.Is(err, ErrResourceNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}
	if errors.Is(err, errNoProductionEnvironment) {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, errNoProductionEnvironment)
	}
	if isPgConstraintViolation(err) {
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
	}
	if err != nil {
		return nil, txError(ctx, "failed to create resource domain", err)
	}

	return &domainv1.CreateResourceDomainResponse{
		DomainId: resourceDomain.String(),
	}, nil
}

// UpdateResourceDomain updates a domain for a resource
func (s *DomainServer) UpdateResourceDomain(
	ctx context.Context,
	req *domainv1.UpdateResourceDomainRequest,
) (*domainv1.UpdateResourceDomainResponse, error) {
	// get the domain to check its resource
	domainID := uuid.MustParse(req.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if err != nil {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDomainNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	// verify user has access to this resource
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateDomain, domainRow.ResourceID.String()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	// check if new domain is available (unless it's the same domain)
	changed := req.GetDomain() != "" && req.GetDomain() != domainRow.Domain
	var subdomainLabel *string
	var platformDomain *string
	if changed {
		available, err := s.queries.CheckDomainAvailability(ctx, req.GetDomain())
		if err != nil {
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}
		if !available {
			return nil, rpcerr.New(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
		}

		if domainRow.DomainSource == genDb.DomainSourcePlatformProvided {
			platform, label, checkErr := s.checkResourceDomain(ctx, domainRow.ResourceID, req.GetDomain())
			if checkErr != nil {
				return nil, checkErr
			}
			subdomainLabel = &label
			platformDomain = &platform
		}
	}

	txErr := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceEnvironments(ctx, qtx, domainRow.ResourceID); lockErr != nil {
			return lockErr
		}
		if changed {
			_, updateErr := qtx.UpdateResourceDomain(ctx, genDb.UpdateResourceDomainParams{
				ID:             domainID,
				Domain:         req.GetDomain(),
				SubdomainLabel: subdomainLabel,
				PlatformDomain: platformDomain,
			})
			if isPgConstraintViolation(updateErr) {
				return rpcerr.New(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
			}
			if updateErr != nil {
				return updateErr
			}
			if routeErr := s.refreshDomainRouting(
				ctx,
				qtx,
				domainRow.ResourceID,
				domainRow.EnvironmentID,
			); routeErr != nil {
				return routeErr
			}
			if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, domainRow.ResourceID); bumpErr != nil {
				return bumpErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DomainUpdated,
			ResourceID:  new(domainRow.ResourceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(domainID),
			Data:        map[string]any{events.FieldDomain: req.GetDomain()},
		})
	})
	if txErr != nil {
		return nil, txError(ctx, "failed to update resource domain", txErr)
	}

	return &domainv1.UpdateResourceDomainResponse{
		DomainId: req.GetDomainId(),
	}, nil
}

// SetPrimaryResourceDomain sets which domain is primary for a resource
func (s *DomainServer) SetPrimaryResourceDomain(
	ctx context.Context,
	req *domainv1.SetPrimaryResourceDomainRequest,
) (*domainv1.SetPrimaryResourceDomainResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.SetPrimaryDomain, req.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	resourceID := uuid.MustParse(req.GetResourceId())
	domainID := uuid.MustParse(req.GetDomainId())

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	lockErr := lockResourceDomains(ctx, qtx, resourceID)
	if errors.Is(lockErr, ErrResourceNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}
	if lockErr != nil {
		slog.ErrorContext(ctx, "failed to lock resource", "resourceId", resourceID, "error", lockErr)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	domainRow, err := qtx.GetResourceDomainByID(ctx, domainID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && domainRow.ResourceID != resourceID) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDomainNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get domain", "domainId", domainID, "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	clearParams := genDb.UpdateResourceDomainPrimaryParams{
		ResourceID:    resourceID,
		EnvironmentID: domainRow.EnvironmentID,
	}
	if clearErr := qtx.UpdateResourceDomainPrimary(ctx, clearParams); clearErr != nil {
		slog.ErrorContext(ctx, "failed to clear primary domain", "resourceId", resourceID, "error", clearErr)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	_, err = qtx.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
		ID:            domainID,
		ResourceID:    resourceID,
		EnvironmentID: domainRow.EnvironmentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDomainNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to set primary domain", "domainId", domainID, "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := s.refreshDomainRouting(ctx, qtx, resourceID, domainRow.EnvironmentID); err != nil {
		return nil, txError(ctx, "failed to update domain routing", err)
	}
	if err := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); err != nil {
		slog.ErrorContext(ctx, "failed to bump environment revisions", "resourceId", resourceID, "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.DomainUpdated,
		ResourceID:  new(resourceID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(domainID),
		Data:        map[string]any{events.FieldPrimary: true},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record primary domain change", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit primary domain change", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	return &domainv1.SetPrimaryResourceDomainResponse{
		ResourceId: req.GetResourceId(),
		DomainId:   req.GetDomainId(),
	}, nil
}

// DeleteResourceDomain removes a domain from a resource
func (s *DomainServer) DeleteResourceDomain(
	ctx context.Context,
	req *domainv1.DeleteResourceDomainRequest,
) (*domainv1.DeleteResourceDomainResponse, error) {
	// get the domain to check its resource and whether it's primary
	domainID := uuid.MustParse(req.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if err != nil {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDomainNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.RemoveDomain, domainRow.ResourceID.String()),
	); verifyErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceDomains(ctx, qtx, domainRow.ResourceID); lockErr != nil {
			return lockErr
		}
		current, getErr := qtx.GetResourceDomainByID(ctx, domainID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return ErrDomainNotFound
		}
		if getErr != nil {
			return fmt.Errorf("get domain: %w", getErr)
		}
		if current.IsPrimary {
			count, countErr := qtx.GetResourceDomainCount(ctx, genDb.GetResourceDomainCountParams{
				ResourceID:    current.ResourceID,
				EnvironmentID: current.EnvironmentID,
			})
			if countErr != nil {
				return fmt.Errorf("count resource domains: %w", countErr)
			}
			if count > 1 {
				return ErrCannotRemovePrimary
			}
		}
		if deleteErr := qtx.DeleteResourceDomain(ctx, domainID); deleteErr != nil {
			return deleteErr
		}
		if routeErr := s.refreshDomainRouting(ctx, qtx, current.ResourceID, current.EnvironmentID); routeErr != nil {
			return routeErr
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, current.ResourceID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DomainDeleted,
			ResourceID:  new(current.ResourceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(domainID),
			Data:        map[string]any{events.FieldDomain: current.Domain},
		})
	})
	if errors.Is(err, ErrDomainNotFound) || errors.Is(err, ErrResourceNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDomainNotFound)
	}
	if errors.Is(err, ErrCannotRemovePrimary) {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrCannotRemovePrimary)
	}
	if err != nil {
		return nil, txError(ctx, "failed to delete resource domain", err)
	}

	return &domainv1.DeleteResourceDomainResponse{}, nil
}

// CheckDomainAvailability checks if a domain is available
func (s *DomainServer) CheckDomainAvailability(
	ctx context.Context,
	req *domainv1.CheckDomainAvailabilityRequest,
) (*domainv1.CheckDomainAvailabilityResponse, error) {
	result, err := s.queries.CheckDomainAvailability(ctx, req.GetDomain())
	if err != nil {
		slog.ErrorContext(ctx, "failed to check domain availability", "domain", req.GetDomain(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to check domain availability")
	}
	slog.InfoContext(ctx, "domain availability check", "domain", req.GetDomain(), "available", result)
	return &domainv1.CheckDomainAvailabilityResponse{
		IsAvailable: result,
	}, nil
}

func (s *DomainServer) checkResourceDomain(
	ctx context.Context,
	resourceID uuid.UUID,
	hostname string,
) (string, string, error) {
	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err, "resourceId", resourceID)
		return "", "", rpcerr.New(connect.CodeInternal, ErrDB)
	}
	env, err := s.queries.GetWorkspaceProductionEnvironment(ctx, resource.WorkspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", rpcerr.New(connect.CodeFailedPrecondition, errNoProductionEnvironment)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get the production environment", "error", err)
		return "", "", rpcerr.New(connect.CodeInternal, ErrDB)
	}
	spec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		slog.ErrorContext(ctx, "failed to decode resource spec", "error", err, "resourceId", resourceID)
		return "", "", rpcerr.New(connect.CodeInternal, ErrDB)
	}
	placements, err := s.queries.ListResourceEnvironmentPlacements(
		ctx,
		genDb.ListResourceEnvironmentPlacementsParams{ResourceID: resourceID, EnvironmentID: env.ID},
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list placements", "error", err, "resourceId", resourceID)
		return "", "", rpcerr.New(connect.CodeInternal, ErrDB)
	}
	placed := make([]uuid.UUID, 0, len(placements))
	for _, placement := range placements {
		placed = append(placed, placement.ClusterID)
	}
	regions := enabledRegions(spec.GetService().GetRegions())
	check, err := resourceDomainCheck(ctx, s.queries, env.EnvironmentType, regions, placed, s.silentAfter)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list domain clusters", "error", err)
		return "", "", rpcerr.New(connect.CodeInternal, ErrDB)
	}
	return check.check(hostname)
}

// imperativeDomainEnvironment is the environment a domain added through the domain or resource
// RPCs belongs to: the oldest production environment of the resource's workspace. Those RPCs
// carry no environment; loco.yaml applies place domains in the environment they target.
func imperativeDomainEnvironment(ctx context.Context, q genDb.Querier, resourceID uuid.UUID) (uuid.UUID, error) {
	workspaceID, err := q.GetResourceWorkspaceID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, ErrResourceNotFound
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("get resource workspace: %w", err)
	}
	return workspaceProductionEnvironment(ctx, q, workspaceID)
}

func workspaceProductionEnvironment(ctx context.Context, q genDb.Querier, workspaceID uuid.UUID) (uuid.UUID, error) {
	env, err := q.GetWorkspaceProductionEnvironment(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, errNoProductionEnvironment
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("get production environment: %w", err)
	}
	return env.ID, nil
}

func lockResourceDomains(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	if err := lockResourceEnvironments(ctx, qtx, resourceID); err != nil {
		return err
	}
	return lockResource(ctx, qtx, resourceID)
}

func (s *DomainServer) refreshDomainRouting(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
) error {
	placements, err := qtx.ListResourceEnvironmentPlacements(
		ctx,
		genDb.ListResourceEnvironmentPlacementsParams{ResourceID: resourceID, EnvironmentID: environmentID},
	)
	if err != nil {
		return fmt.Errorf("list domain placements: %w", err)
	}
	if len(placements) == 0 {
		return nil
	}
	hostname, err := primaryHostname(ctx, qtx, resourceID, environmentID)
	if err != nil {
		return err
	}
	resource, err := qtx.GetResourceByID(ctx, resourceID)
	if err != nil {
		return err
	}
	resourceSpec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		return err
	}
	for _, placement := range placements {
		var payload ApplicationPayload
		if err := json.Unmarshal(placement.DesiredSpec, &payload); err != nil {
			return fmt.Errorf("decode domain placement: %w", err)
		}
		if payload.AppSpec == nil || payload.AppSpec.ServiceSpec == nil || placement.DeploymentID == nil {
			return errResourceSpecNotService
		}
		service := payload.AppSpec.ServiceSpec
		if service.Routing == nil && hostname == "" {
			continue
		}
		if service.Routing != nil && service.Routing.HostName == hostname {
			continue
		}
		switch {
		case hostname == "":
			service.Routing = nil
		case service.Routing != nil:
			service.Routing.HostName = hostname
		default:
			deployment, err := qtx.GetDeploymentByID(ctx, *placement.DeploymentID)
			if err != nil {
				return err
			}
			deploymentService := &deploymentv1.ServiceDeploymentSpec{}
			if err := protojson.Unmarshal(deployment.Spec, deploymentService); err != nil {
				return fmt.Errorf("decode deployment routing: %w", err)
			}
			routing := deploymentRouting(resourceSpec.GetService(), deploymentService)
			service.Routing = converter.ProtoToRoutingSpec(routing, hostname, s.defaults)
		}
		desiredSpec, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode domain placement: %w", err)
		}
		params := genDb.UpsertPlacementParams{
			ResourceID:    placement.ResourceID,
			ClusterID:     placement.ClusterID,
			Region:        placement.Region,
			DeploymentID:  placement.DeploymentID,
			DesiredSpec:   desiredSpec,
			EnvironmentID: placement.EnvironmentID,
			SecretNames:   placement.SecretNames,
		}
		if err := placeApplication(ctx, qtx, params); err != nil {
			return err
		}
	}
	return nil
}
