package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	domainv1 "github.com/team-loco/loco/shared/proto/domain/v1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
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
) (*connect.Response[domainv1.PlatformDomain], error) {
	r := req.Msg

	if r.Domain == "" {
		slog.ErrorContext(ctx, "invalid request: domain is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain is required"))
	}

	result, err := s.queries.CreatePlatformDomain(ctx, genDb.CreatePlatformDomainParams{
		Domain:   r.Domain,
		IsActive: r.IsActive,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create platform domain", "domain", r.Domain, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create platform domain: %w", err))
	}

	domain, err := s.queries.GetPlatformDomain(ctx, result)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get created platform domain", "id", result, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get created platform domain: %w", err))
	}

	return connect.NewResponse(&domainv1.PlatformDomain{
		Id:        domain.ID,
		Domain:    domain.Domain,
		IsActive:  domain.IsActive,
		CreatedAt: timestamppb.New(domain.CreatedAt.Time),
		UpdatedAt: timestamppb.New(domain.CreatedAt.Time),
	}), nil
}

// GetPlatformDomain retrieves a platform domain by ID or name (public - used for domain selection)
func (s *DomainServer) GetPlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.GetPlatformDomainRequest],
) (*connect.Response[domainv1.PlatformDomain], error) {
	r := req.Msg

	var result genDb.PlatformDomain
	var err error

	if r.Id != nil && *r.Id > 0 {
		result, err = s.queries.GetPlatformDomain(ctx, *r.Id)
	} else if r.Domain != nil && *r.Domain != "" {
		result, err = s.queries.GetPlatformDomainByName(ctx, *r.Domain)
	} else {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("either id or domain must be provided"))
	}

	if err != nil {
		slog.ErrorContext(ctx, "failed to get platform domain", "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}

	return connect.NewResponse(&domainv1.PlatformDomain{
		Id:        result.ID,
		Domain:    result.Domain,
		IsActive:  result.IsActive,
		CreatedAt: timestamppb.New(result.CreatedAt.Time),
		UpdatedAt: timestamppb.New(result.CreatedAt.Time),
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

	if r.ActiveOnly != nil && *r.ActiveOnly {
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
			Id:        result.ID,
			Domain:    result.Domain,
			IsActive:  result.IsActive,
			CreatedAt: timestamppb.New(result.CreatedAt.Time),
			UpdatedAt: timestamppb.New(result.CreatedAt.Time),
		}
	}

	return connect.NewResponse(&domainv1.ListPlatformDomainsResponse{
		PlatformDomains: domains,
		TotalCount:      int64(len(domains)),
	}), nil
}

// UpdatePlatformDomain updates a platform domain
func (s *DomainServer) UpdatePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.UpdatePlatformDomainRequest],
) (*connect.Response[domainv1.PlatformDomain], error) {
	r := req.Msg

	if r.Id <= 0 {
		slog.ErrorContext(ctx, "invalid request: id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	// For now, we'll update using the existing deactivate method if is_active is being changed
	// This is a simplified implementation
	if r.IsActive != nil && !*r.IsActive {
		_, err := s.queries.DeactivatePlatformDomain(ctx, r.Id)
		if err != nil {
			slog.ErrorContext(ctx, "failed to update platform domain", "id", r.Id, "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update platform domain: %w", err))
		}
	}

	result, err := s.queries.GetPlatformDomain(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get platform domain", "id", r.Id, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}

	return connect.NewResponse(&domainv1.PlatformDomain{
		Id:        result.ID,
		Domain:    result.Domain,
		IsActive:  result.IsActive,
		CreatedAt: timestamppb.New(result.CreatedAt.Time),
		UpdatedAt: timestamppb.New(result.CreatedAt.Time),
	}), nil
}

// DeletePlatformDomain deletes a platform domain
func (s *DomainServer) DeletePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.DeletePlatformDomainRequest],
) (*connect.Response[emptypb.Empty], error) {
	r := req.Msg

	if r.Id <= 0 {
		slog.ErrorContext(ctx, "invalid request: id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	// Use deactivate for now as delete equivalent
	_, err := s.queries.DeactivatePlatformDomain(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete platform domain", "id", r.Id, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete platform domain: %w", err))
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// ListLocoOwnedDomains lists all loco-owned (subdomain) domains (admin only)
func (s *DomainServer) ListLocoOwnedDomains(
	ctx context.Context,
	req *connect.Request[domainv1.ListLocoOwnedDomainsRequest],
) (*connect.Response[domainv1.ListLocoOwnedDomainsResponse], error) {
	results, err := s.queries.ListAllLocoOwnedDomains(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list loco owned domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list loco owned domains: %w", err))
	}

	domains := make([]*domainv1.LocoOwnedDomain, len(results))
	for i, result := range results {
		domains[i] = &domainv1.LocoOwnedDomain{
			Id:             result.ID,
			Domain:         result.Domain,
			ResourceName:   result.ResourceName,
			ResourceId:     result.ResourceID,
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
) (*connect.Response[domainv1.ResourceDomain], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.AddDomain, r.ResourceId)); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if r.Domain == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain input is required"))
	}

	// extract and validate domain information based on source
	var fullDomain string
	var subdomainLabel pgtype.Text
	platformDomainID := pgtype.Int8{Valid: false}
	domainSource := genDb.DomainSourceUserProvided

	if r.Domain.DomainSource == domainv1.DomainType_PLATFORM_PROVIDED {
		if r.Domain.Subdomain == nil || *r.Domain.Subdomain == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subdomain required for platform-provided domains"))
		}
		if r.Domain.PlatformDomainId == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("platform_domain_id required for platform_provided domains"))
		}

		platformDomainID = pgtype.Int8{Int64: *r.Domain.PlatformDomainId, Valid: true}
		platformDomain, err := s.queries.GetPlatformDomain(ctx, *r.Domain.PlatformDomainId)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
		}

		fullDomain = *r.Domain.Subdomain + "." + platformDomain.Domain
		subdomainLabel = pgtype.Text{String: *r.Domain.Subdomain, Valid: true}
		domainSource = genDb.DomainSourcePlatformProvided
	} else {
		if r.Domain.Domain == nil || *r.Domain.Domain == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain required for user-provided domains"))
		}
		fullDomain = *r.Domain.Domain
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
	count, err := s.queries.GetResourceDomainCount(ctx, r.ResourceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	domainID, err := s.queries.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
		ResourceID:       r.ResourceId,
		Domain:           fullDomain,
		DomainSource:     domainSource,
		SubdomainLabel:   subdomainLabel,
		PlatformDomainID: platformDomainID,
		IsPrimary:        count == 0, // first domain is primary
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// Get the created domain to return
	createdDomain, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	protoSource := domainv1.DomainType_USER_PROVIDED
	if createdDomain.DomainSource == genDb.DomainSourcePlatformProvided {
		protoSource = domainv1.DomainType_PLATFORM_PROVIDED
	}

	result := &domainv1.ResourceDomain{
		Id:           createdDomain.ID,
		ResourceId:   createdDomain.ResourceID,
		Domain:       createdDomain.Domain,
		DomainSource: protoSource,
		IsPrimary:    createdDomain.IsPrimary,
		CreatedAt:    timestamppb.New(createdDomain.CreatedAt.Time),
		UpdatedAt:    timestamppb.New(createdDomain.UpdatedAt.Time),
	}

	if createdDomain.SubdomainLabel.Valid {
		result.SubdomainLabel = &createdDomain.SubdomainLabel.String
	}
	if createdDomain.PlatformDomainID.Valid {
		result.PlatformDomainId = &createdDomain.PlatformDomainID.Int64
	}

	return connect.NewResponse(result), nil
}

// UpdateResourceDomain updates a domain for a resource
func (s *DomainServer) UpdateResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.UpdateResourceDomainRequest],
) (*connect.Response[domainv1.ResourceDomain], error) {
	r := req.Msg

	// get the domain to check its resource
	domainRow, err := s.queries.GetResourceDomainByID(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found"))
	}

	// verify user has access to this resource
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.UpdateDomain, domainRow.ResourceID)); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// check if new domain is available (unless it's the same domain)
	if r.Domain != nil && *r.Domain != domainRow.Domain {
		available, err := s.queries.CheckDomainAvailability(ctx, *r.Domain)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		if !available {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
		}

		// update the domain
		_, err = s.queries.UpdateResourceDomain(ctx, genDb.UpdateResourceDomainParams{
			ID:     r.DomainId,
			Domain: *r.Domain,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to update resource domain", "id", r.DomainId, "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
	}

	// Get updated domain
	updatedDomain, err := s.queries.GetResourceDomainByID(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	protoSource := domainv1.DomainType_USER_PROVIDED
	if updatedDomain.DomainSource == genDb.DomainSourcePlatformProvided {
		protoSource = domainv1.DomainType_PLATFORM_PROVIDED
	}

	result := &domainv1.ResourceDomain{
		Id:           updatedDomain.ID,
		ResourceId:   updatedDomain.ResourceID,
		Domain:       updatedDomain.Domain,
		DomainSource: protoSource,
		IsPrimary:    updatedDomain.IsPrimary,
		CreatedAt:    timestamppb.New(updatedDomain.CreatedAt.Time),
		UpdatedAt:    timestamppb.New(updatedDomain.UpdatedAt.Time),
	}

	if updatedDomain.SubdomainLabel.Valid {
		result.SubdomainLabel = &updatedDomain.SubdomainLabel.String
	}
	if updatedDomain.PlatformDomainID.Valid {
		result.PlatformDomainId = &updatedDomain.PlatformDomainID.Int64
	}

	return connect.NewResponse(result), nil
}

// SetPrimaryResourceDomain sets which domain is primary for a resource
func (s *DomainServer) SetPrimaryResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.SetPrimaryResourceDomainRequest],
) (*connect.Response[domainv1.ResourceDomain], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.SetPrimaryDomain, r.ResourceId)); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// unset primary on all other domains
	err := s.queries.UpdateResourceDomainPrimary(ctx, r.ResourceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// set this domain as primary
	_, err = s.queries.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
		ID:         r.DomainId,
		ResourceID: r.ResourceId,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found or does not belong to resource"))
	}

	// Get updated domain
	updatedDomain, err := s.queries.GetResourceDomainByID(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	protoSource := domainv1.DomainType_USER_PROVIDED
	if updatedDomain.DomainSource == genDb.DomainSourcePlatformProvided {
		protoSource = domainv1.DomainType_PLATFORM_PROVIDED
	}

	result := &domainv1.ResourceDomain{
		Id:           updatedDomain.ID,
		ResourceId:   updatedDomain.ResourceID,
		Domain:       updatedDomain.Domain,
		DomainSource: protoSource,
		IsPrimary:    updatedDomain.IsPrimary,
		CreatedAt:    timestamppb.New(updatedDomain.CreatedAt.Time),
		UpdatedAt:    timestamppb.New(updatedDomain.UpdatedAt.Time),
	}

	if updatedDomain.SubdomainLabel.Valid {
		result.SubdomainLabel = &updatedDomain.SubdomainLabel.String
	}
	if updatedDomain.PlatformDomainID.Valid {
		result.PlatformDomainId = &updatedDomain.PlatformDomainID.Int64
	}

	return connect.NewResponse(result), nil
}

// DeleteResourceDomain removes a domain from a resource
func (s *DomainServer) DeleteResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.DeleteResourceDomainRequest],
) (*connect.Response[emptypb.Empty], error) {
	r := req.Msg

	// get the domain to check its resource and whether it's primary
	domainRow, err := s.queries.GetResourceDomainByID(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.RemoveDomain, domainRow.ResourceID)); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
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
	err = s.queries.DeleteResourceDomain(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// CheckDomainAvailability checks if a domain is available
func (s *DomainServer) CheckDomainAvailability(
	ctx context.Context,
	req *connect.Request[domainv1.CheckDomainAvailabilityRequest],
) (*connect.Response[domainv1.CheckDomainAvailabilityResponse], error) {
	r := req.Msg

	if r.Domain == "" {
		slog.ErrorContext(ctx, "invalid request: domain is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain is required"))
	}

	result, err := s.queries.CheckDomainAvailability(ctx, r.Domain)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check domain availability", "domain", r.Domain, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to check domain availability: %w", err))
	}
	slog.InfoContext(ctx, "domain availability check", "domain", r.Domain, "available", result)
	return &connect.Response[domainv1.CheckDomainAvailabilityResponse]{
		Msg: &domainv1.CheckDomainAvailabilityResponse{
			IsAvailable: result,
		},
	}, nil
}
