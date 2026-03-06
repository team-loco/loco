package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	domainv1 "github.com/team-loco/loco/proto/loco/domain/v1"
)

var (
	ErrPlatformDomainNotFound = errors.New("platform domain not found")
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.NewSystem(actions.CreatePlatformDomain)); err != nil {
		slog.WarnContext(ctx, "unauthorized to create platform domain")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	platformDomain, err := s.queries.CreatePlatformDomain(ctx, genDb.CreatePlatformDomainParams{
		Domain:   r.GetDomain(),
		IsActive: r.GetIsActive(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create platform domain", "domain", r.GetDomain(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create platform domain: %w", err))
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
		// If we need to list all domains, we'd need a new query
		// For now, fall back to active only
		results, err = s.queries.ListActivePlatformDomains(ctx)
	}

	if err != nil {
		slog.ErrorContext(ctx, "failed to list platform domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list platform domains: %w", err))
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.NewSystem(actions.UpdatePlatformDomain)); err != nil {
		slog.WarnContext(ctx, "unauthorized to update platform domain")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	parsedID := uuid.MustParse(r.GetId())

	// For now, we'll update using the existing deactivate method if is_active is being changed
	// This is a simplified implementation
	if !r.GetIsActive() {
		_, err := s.queries.DeactivatePlatformDomain(ctx, parsedID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to update platform domain", "id", r.GetId(), "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update platform domain: %w", err))
		}
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.NewSystem(actions.DeletePlatformDomain)); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete platform domain")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	parsedID := uuid.MustParse(r.GetId())

	// Use deactivate for now as delete equivalent
	_, err := s.queries.DeactivatePlatformDomain(ctx, parsedID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete platform domain", "id", r.GetId(), "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete platform domain: %w", err))
	}

	return connect.NewResponse(&domainv1.DeletePlatformDomainResponse{}), nil
}

// ListLocoOwnedDomains lists all loco-owned (subdomain) domains (admin only)
func (s *DomainServer) ListLocoOwnedDomains(
	ctx context.Context,
	req *connect.Request[domainv1.ListLocoOwnedDomainsRequest],
) (*connect.Response[domainv1.ListLocoOwnedDomainsResponse], error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.NewSystem(actions.ListLocoOwnedDomains)); err != nil {
		slog.WarnContext(ctx, "unauthorized to list loco owned domains")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	results, err := s.queries.ListAllLocoOwnedDomains(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list loco owned domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list loco owned domains: %w", err))
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.AddDomain, r.GetResourceId())); err != nil {
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if !available {
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
	}

	// check if this is the first domain for the resource
	resourceId := uuid.MustParse(r.ResourceId)

	count, err := s.queries.GetResourceDomainCount(ctx, resourceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	resourceDomain, err := s.queries.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
		ResourceID:       resourceId,
		Domain:           fullDomain,
		DomainSource:     domainSource,
		SubdomainLabel:   subdomainLabel,
		PlatformDomainID: platformDomainID,
		IsPrimary:        count == 0, // first domain is primary
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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
	domainId := uuid.MustParse(r.DomainId)

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found"))
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	// verify user has access to this resource
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.UpdateDomain, domainRow.ResourceID.String())); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// check if new domain is available (unless it's the same domain)
	if r.GetDomain() != "" && r.GetDomain() != domainRow.Domain {
		available, err := s.queries.CheckDomainAvailability(ctx, r.GetDomain())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		if !available {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
		}

		// update the domain
		_, err = s.queries.UpdateResourceDomain(ctx, genDb.UpdateResourceDomainParams{
			ID:     domainId,
			Domain: r.GetDomain(),
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to update resource domain", "id", r.GetDomainId(), "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
	}

	return connect.NewResponse(&domainv1.UpdateResourceDomainResponse{
		DomainId: r.GetDomainId(),
	}), nil
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.SetPrimaryDomain, r.GetResourceId())); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// unset primary on all other domains
	resourceId := uuid.MustParse(r.GetResourceId())

	err := s.queries.UpdateResourceDomainPrimary(ctx, resourceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// set this domain as primary
	domainId := uuid.MustParse(r.GetDomainId())

	_, err = s.queries.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
		ID:         domainId,
		ResourceID: resourceId,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found or does not belong to resource"))
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
	domainId := uuid.MustParse(r.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found"))
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if verifyErr := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.RemoveDomain, domainRow.ResourceID.String())); verifyErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	// cannot remove primary domain
	if domainRow.IsPrimary {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrCannotRemovePrimary)
	}

	// cannot remove if it's the only domain
	count, err := s.queries.GetResourceDomainCount(ctx, domainRow.ResourceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if count <= 1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrCannotRemoveOnly)
	}

	// delete the domain
	err = s.queries.DeleteResourceDomain(ctx, domainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to check domain availability: %w", err))
	}
	slog.InfoContext(ctx, "domain availability check", "domain", r.GetDomain(), "available", result)
	return &connect.Response[domainv1.CheckDomainAvailabilityResponse]{
		Msg: &domainv1.CheckDomainAvailabilityResponse{
			IsAvailable: result,
		},
	}, nil
}
