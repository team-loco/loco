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
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
)

var (
	ErrPlatformDomainNotFound = errors.New("platform domain not found")
	ErrPlatformDomainUpdate   = errors.New("failed to update platform domain")
	ErrDomainAlreadyExists    = errors.New("domain already exists")
	ErrCannotRemovePrimary    = errors.New("cannot remove primary domain")
	ErrCannotRemoveOnly       = errors.New("cannot remove resource's only domain")
)

type DomainServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	machine *tvm.VendingMachine
}

func NewDomainServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *DomainServer {
	return &DomainServer{db: db, queries: queries, machine: machine}
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

	if err := s.machine.VerifyWithGivenEntityScopes(
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

	if err := s.machine.VerifyWithGivenEntityScopes(
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

	if err := s.machine.VerifyWithGivenEntityScopes(
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

	if err := s.machine.VerifyWithGivenEntityScopes(
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

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.AddDomain, r.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	// extract and validate domain information based on source
	var fullDomain string
	var subdomainLabel *string
	var platformDomainID *uuid.UUID
	domainSource := genDb.DomainSourceUserProvided

	if r.GetDomain().GetDomainSource() == domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED {
		parsedPlatformDomainID := uuid.MustParse(r.GetDomain().GetPlatformDomainId())
		platformDomainID = &parsedPlatformDomainID
		platformDomain, err := s.queries.GetPlatformDomain(ctx, parsedPlatformDomainID)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
		}

		fullDomain = r.GetDomain().GetSubdomain() + "." + platformDomain.Domain
		subdomain := r.GetDomain().GetSubdomain()
		subdomainLabel = &subdomain
		domainSource = genDb.DomainSourcePlatformProvided
	} else {
		fullDomain = r.GetDomain().GetDomain()
	}

	// check domain availability
	available, err := s.queries.CheckDomainAvailability(ctx, fullDomain)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if !available {
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
	}

	// check if this is the first domain for the resource
	resourceID := uuid.MustParse(r.GetResourceId())

	count, err := s.queries.GetResourceDomainCount(ctx, resourceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var resourceDomain uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		created, createErr := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
			ResourceID:       resourceID,
			Domain:           fullDomain,
			DomainSource:     domainSource,
			SubdomainLabel:   subdomainLabel,
			PlatformDomainID: platformDomainID,
			IsPrimary:        count == 0, // first domain is primary
		})
		if isPgConstraintViolation(createErr) {
			return connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
		}
		if createErr != nil {
			return createErr
		}
		resourceDomain = created
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DomainCreated,
			ResourceID:  new(resourceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(resourceDomain),
			Data:        map[string]any{events.FieldDomain: fullDomain, events.FieldResourceID: resourceID.String()},
		})
	})
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
	if err := s.machine.VerifyWithGivenEntityScopes(
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

	if err := s.machine.VerifyWithGivenEntityScopes(
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

	if clearErr := qtx.UpdateResourceDomainPrimary(ctx, resourceID); clearErr != nil {
		slog.ErrorContext(ctx, "failed to clear primary domain", "resourceId", resourceID, "error", clearErr)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	_, err = qtx.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
		ID:         domainID,
		ResourceID: resourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(
			connect.CodeNotFound,
			errors.New("domain not found or does not belong to resource"),
		)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to set primary domain", "domainId", domainID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.DomainUpdated,
		ResourceID:  new(resourceID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(domainID),
		Data:        map[string]any{"primary": true},
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

	if verifyErr := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.RemoveDomain, domainRow.ResourceID.String()),
	); verifyErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	// cannot remove primary domain
	if domainRow.IsPrimary {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrCannotRemovePrimary)
	}

	// cannot remove if it's the only domain
	count, err := s.queries.GetResourceDomainCount(ctx, domainRow.ResourceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if count <= 1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrCannotRemoveOnly)
	}

	// delete the domain
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if deleteErr := qtx.DeleteResourceDomain(ctx, domainID); deleteErr != nil {
			return deleteErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DomainDeleted,
			ResourceID:  new(domainRow.ResourceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(domainID),
			Data:        map[string]any{events.FieldDomain: domainRow.Domain},
		})
	})
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
