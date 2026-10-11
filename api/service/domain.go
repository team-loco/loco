package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/team-loco/loco/api/events"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
)

var (
	ErrPlatformDomainNotFound  = errors.New("platform domain not found")
	ErrPlatformDomainUpdate    = errors.New("failed to update platform domain")
	ErrDomainAlreadyExists     = errors.New("domain already exists")
	ErrCannotRemovePrimary     = errors.New("make another domain primary before removing the primary domain")
	errNoProductionEnvironment = errors.New("the workspace has no production environment to hold the domain")
)

type DomainServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	machine *tvm.VendingMachine
}

func NewDomainServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *DomainServer {
	return &DomainServer{db: db, queries: queries, machine: machine, authz: authz.New(db, queries)}
}

// CreatePlatformDomain creates a new platform domain (admin only)
func (s *DomainServer) CreatePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.CreatePlatformDomainRequest],
) (*connect.Response[domainv1.CreatePlatformDomainResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.NewSystem(actions.CreatePlatformDomain),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to create platform domain")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	var platformDomain uuid.UUID
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		created, createErr := qtx.CreatePlatformDomain(ctx, genDb.CreatePlatformDomainParams{
			Domain:   r.GetDomain(),
			IsActive: r.GetIsActive(),
		})
		if createErr != nil {
			return createErr
		}
		platformDomain = created
		return events.Record(ctx, qtx, events.Event{
			Type:        events.PlatformDomainChange,
			SubjectType: events.SubjectPlatformDomain,
			SubjectID:   new(platformDomain),
			Data:        map[string]any{events.FieldAction: "created", events.FieldDomain: r.GetDomain()},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create platform domain", "domain", r.GetDomain(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create platform domain"))
	}

	return connect.NewResponse(&domainv1.CreatePlatformDomainResponse{
		Id: platformDomain.String(),
	}), nil
}

// GetPlatformDomain retrieves a platform domain by ID or name (public - used for domain selection)
func (s *DomainServer) GetPlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.GetPlatformDomainRequest],
) (*connect.Response[domainv1.GetPlatformDomainResponse], error) {
	r := req.Msg

	var result genDb.PlatformDomain
	var err error

	switch key := r.GetKey().(type) {
	case *domainv1.GetPlatformDomainRequest_Id:
		result, err = s.queries.GetPlatformDomain(ctx, uuid.MustParse(key.Id))
	case *domainv1.GetPlatformDomainRequest_Domain:
		result, err = s.queries.GetPlatformDomainByName(ctx, key.Domain)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("either id or domain must be provided"))
	}

	if err != nil {
		slog.ErrorContext(ctx, "failed to get platform domain", "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}

	return connect.NewResponse(&domainv1.GetPlatformDomainResponse{
		PlatformDomain: &domainv1.PlatformDomain{
			Id:        result.ID.String(),
			Domain:    result.Domain,
			IsActive:  result.IsActive,
			CreatedAt: timeutil.ParsePostgresTimestamp(result.CreatedAt),
		},
	}), nil
}

// ListPlatformDomains lists platform domains with optional filters
func (s *DomainServer) ListPlatformDomains(
	ctx context.Context,
	req *connect.Request[domainv1.ListPlatformDomainsRequest],
) (*connect.Response[domainv1.ListPlatformDomainsResponse], error) {
	r := req.Msg

	var results []genDb.PlatformDomain
	var err error

	if r.GetActiveOnly() {
		results, err = s.queries.ListActivePlatformDomains(ctx)
	} else {
		results, err = s.queries.ListPlatformDomains(ctx, nil)
	}

	if err != nil {
		slog.ErrorContext(ctx, "failed to list platform domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list platform domains"))
	}

	domains := make([]*domainv1.PlatformDomain, len(results))
	for i, result := range results {
		domains[i] = &domainv1.PlatformDomain{
			Id:        result.ID.String(),
			Domain:    result.Domain,
			IsActive:  result.IsActive,
			CreatedAt: timeutil.ParsePostgresTimestamp(result.CreatedAt),
		}
	}

	return connect.NewResponse(&domainv1.ListPlatformDomainsResponse{
		PlatformDomains: domains,
	}), nil
}

// UpdatePlatformDomain updates a platform domain
func (s *DomainServer) UpdatePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.UpdatePlatformDomainRequest],
) (*connect.Response[domainv1.UpdatePlatformDomainResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.NewSystem(actions.UpdatePlatformDomain),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update platform domain")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	parsedID := uuid.MustParse(r.GetId())

	// For now, we'll update using the existing deactivate method if is_active is being changed
	// This is a simplified implementation
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if !r.GetIsActive() {
			if _, deactivateErr := qtx.DeactivatePlatformDomain(ctx, parsedID); deactivateErr != nil {
				return deactivateErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.PlatformDomainChange,
			SubjectType: events.SubjectPlatformDomain,
			SubjectID:   new(parsedID),
			Data:        map[string]any{events.FieldAction: "updated"},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update platform domain", "id", r.GetId(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrPlatformDomainUpdate)
	}

	return connect.NewResponse(&domainv1.UpdatePlatformDomainResponse{
		Id: r.GetId(),
	}), nil
}

// DeletePlatformDomain deletes a platform domain
func (s *DomainServer) DeletePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.DeletePlatformDomainRequest],
) (*connect.Response[domainv1.DeletePlatformDomainResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.NewSystem(actions.DeletePlatformDomain),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete platform domain")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	parsedID := uuid.MustParse(r.GetId())

	// Use deactivate for now as delete equivalent
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, deactivateErr := qtx.DeactivatePlatformDomain(ctx, parsedID); deactivateErr != nil {
			return deactivateErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.PlatformDomainChange,
			SubjectType: events.SubjectPlatformDomain,
			SubjectID:   new(parsedID),
			Data:        map[string]any{events.FieldAction: "deleted"},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete platform domain", "id", r.GetId(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete platform domain"))
	}

	return connect.NewResponse(&domainv1.DeletePlatformDomainResponse{}), nil
}

// ListLocoOwnedDomains lists all loco-owned (subdomain) domains (admin only)
func (s *DomainServer) ListLocoOwnedDomains(
	ctx context.Context,
	_ *connect.Request[domainv1.ListLocoOwnedDomainsRequest],
) (*connect.Response[domainv1.ListLocoOwnedDomainsResponse], error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.NewSystem(actions.ListLocoOwnedDomains),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list loco owned domains")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	results, err := s.queries.ListAllLocoOwnedDomains(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list loco owned domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list loco owned domains"))
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

	return connect.NewResponse(&domainv1.ListLocoOwnedDomainsResponse{
		Domains: domains,
	}), nil
}

// CreateResourceDomain adds a new domain to a resource
func (s *DomainServer) CreateResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.CreateResourceDomainRequest],
) (*connect.Response[domainv1.CreateResourceDomainResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.AddDomain, r.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if r.GetDomain().GetDomainSource() != domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED {
		return nil, connect.NewError(connect.CodeUnimplemented, errCustomDomainsUnsupported)
	}
	platformDomainID := uuid.MustParse(r.GetDomain().GetPlatformDomainId())
	platformDomain, err := s.queries.GetPlatformDomain(ctx, platformDomainID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}

	subdomain := r.GetDomain().GetSubdomain()
	fullDomain := subdomain + "." + platformDomain.Domain

	// check domain availability
	available, err := s.queries.CheckDomainAvailability(ctx, fullDomain)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if !available {
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
	}

	resourceID := uuid.MustParse(r.GetResourceId())

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
			ResourceID:       resourceID,
			EnvironmentID:    environmentID,
			Domain:           fullDomain,
			DomainSource:     genDb.DomainSourcePlatformProvided,
			SubdomainLabel:   &subdomain,
			PlatformDomainID: &platformDomainID,
			IsPrimary:        !hasPrimary,
		})
		if createErr != nil {
			return createErr
		}
		resourceDomain = created
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
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	if errors.Is(err, errNoProductionEnvironment) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errNoProductionEnvironment)
	}
	if isPgConstraintViolation(err) {
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
	}
	if err != nil {
		return nil, txError(ctx, "failed to create resource domain", err)
	}

	return connect.NewResponse(&domainv1.CreateResourceDomainResponse{
		DomainId: resourceDomain.String(),
	}), nil
}

// UpdateResourceDomain updates a domain for a resource
func (s *DomainServer) UpdateResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.UpdateResourceDomainRequest],
) (*connect.Response[domainv1.UpdateResourceDomainResponse], error) {
	r := req.Msg

	// get the domain to check its resource
	domainID := uuid.MustParse(r.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	// verify user has access to this resource
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateDomain, domainRow.ResourceID.String()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// check if new domain is available (unless it's the same domain)
	changed := r.GetDomain() != "" && r.GetDomain() != domainRow.Domain
	var subdomainLabel *string
	if changed {
		available, err := s.queries.CheckDomainAvailability(ctx, r.GetDomain())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
		if !available {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
		}

		if domainRow.DomainSource == genDb.DomainSourcePlatformProvided {
			label, labelErr := s.platformSubdomainLabel(ctx, domainRow.PlatformDomainID, r.GetDomain())
			if labelErr != nil {
				return nil, labelErr
			}
			subdomainLabel = &label
		}
	}

	txErr := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceEnvironments(ctx, qtx, domainRow.ResourceID); lockErr != nil {
			return lockErr
		}
		if changed {
			_, updateErr := qtx.UpdateResourceDomain(ctx, genDb.UpdateResourceDomainParams{
				ID:             domainID,
				Domain:         r.GetDomain(),
				SubdomainLabel: subdomainLabel,
			})
			if isPgConstraintViolation(updateErr) {
				return connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
			}
			if updateErr != nil {
				return updateErr
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
			Data:        map[string]any{events.FieldDomain: r.GetDomain()},
		})
	})
	if txErr != nil {
		return nil, txError(ctx, "failed to update resource domain", txErr)
	}

	return connect.NewResponse(&domainv1.UpdateResourceDomainResponse{
		DomainId: r.GetDomainId(),
	}), nil
}

func (s *DomainServer) platformSubdomainLabel(
	ctx context.Context,
	platformDomainID *uuid.UUID,
	domain string,
) (string, error) {
	if platformDomainID == nil {
		slog.ErrorContext(ctx, "platform-provided domain has no platform domain id")
		return "", connect.NewError(connect.CodeInternal, ErrDB)
	}
	platformDomain, err := s.queries.GetPlatformDomain(ctx, *platformDomainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get platform domain", "error", err)
		return "", connect.NewError(connect.CodeInternal, ErrDB)
	}
	return subdomainLabelFor(domain, platformDomain.Domain)
}

func subdomainLabelFor(domain string, platformDomain string) (string, error) {
	label, found := strings.CutSuffix(domain, "."+platformDomain)
	if !found || label == "" || strings.Contains(label, ".") {
		return "", connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("domain must be a single label under %s", platformDomain),
		)
	}
	return label, nil
}

// SetPrimaryResourceDomain sets which domain is primary for a resource
func (s *DomainServer) SetPrimaryResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.SetPrimaryResourceDomainRequest],
) (*connect.Response[domainv1.SetPrimaryResourceDomainResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.SetPrimaryDomain, r.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID := uuid.MustParse(r.GetResourceId())
	domainID := uuid.MustParse(r.GetDomainId())

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	lockErr := lockResourceDomains(ctx, qtx, resourceID)
	if errors.Is(lockErr, ErrResourceNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	if lockErr != nil {
		slog.ErrorContext(ctx, "failed to lock resource", "resourceId", resourceID, "error", lockErr)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	domainRow, err := qtx.GetResourceDomainByID(ctx, domainID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && domainRow.ResourceID != resourceID) {
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get domain", "domainId", domainID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	clearParams := genDb.UpdateResourceDomainPrimaryParams{
		ResourceID:    resourceID,
		EnvironmentID: domainRow.EnvironmentID,
	}
	if clearErr := qtx.UpdateResourceDomainPrimary(ctx, clearParams); clearErr != nil {
		slog.ErrorContext(ctx, "failed to clear primary domain", "resourceId", resourceID, "error", clearErr)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	_, err = qtx.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
		ID:            domainID,
		ResourceID:    resourceID,
		EnvironmentID: domainRow.EnvironmentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to set primary domain", "domainId", domainID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); err != nil {
		slog.ErrorContext(ctx, "failed to bump environment revisions", "resourceId", resourceID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.DomainUpdated,
		ResourceID:  new(resourceID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(domainID),
		Data:        map[string]any{events.FieldPrimary: true},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record primary domain change", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit primary domain change", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&domainv1.SetPrimaryResourceDomainResponse{
		ResourceId: r.GetResourceId(),
		DomainId:   r.GetDomainId(),
	}), nil
}

// DeleteResourceDomain removes a domain from a resource
func (s *DomainServer) DeleteResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.DeleteResourceDomainRequest],
) (*connect.Response[domainv1.DeleteResourceDomainResponse], error) {
	r := req.Msg

	// get the domain to check its resource and whether it's primary
	domainID := uuid.MustParse(r.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.RemoveDomain, domainRow.ResourceID.String()),
	); verifyErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
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
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}
	if errors.Is(err, ErrCannotRemovePrimary) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrCannotRemovePrimary)
	}
	if err != nil {
		return nil, txError(ctx, "failed to delete resource domain", err)
	}

	return connect.NewResponse(&domainv1.DeleteResourceDomainResponse{}), nil
}

// CheckDomainAvailability checks if a domain is available
func (s *DomainServer) CheckDomainAvailability(
	ctx context.Context,
	req *connect.Request[domainv1.CheckDomainAvailabilityRequest],
) (*connect.Response[domainv1.CheckDomainAvailabilityResponse], error) {
	r := req.Msg

	result, err := s.queries.CheckDomainAvailability(ctx, r.GetDomain())
	if err != nil {
		slog.ErrorContext(ctx, "failed to check domain availability", "domain", r.GetDomain(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to check domain availability"))
	}
	slog.InfoContext(ctx, "domain availability check", "domain", r.GetDomain(), "available", result)
	return &connect.Response[domainv1.CheckDomainAvailabilityResponse]{
		Msg: &domainv1.CheckDomainAvailabilityResponse{
			IsAvailable: result,
		},
	}, nil
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
