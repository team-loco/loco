package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/loco-team/loco/api/contextkeys"
	genDb "github.com/loco-team/loco/api/gen/db"
	domainv1 "github.com/loco-team/loco/shared/proto/domain/v1"
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
}

func NewDomainServer(db *pgxpool.Pool, queries genDb.Querier) *DomainServer {
	return &DomainServer{db: db, queries: queries}
}

// CreatePlatformDomain creates a new platform domain
func (s *DomainServer) CreatePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.CreatePlatformDomainRequest],
) (*connect.Response[domainv1.CreatePlatformDomainResponse], error) {
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

	return &connect.Response[domainv1.CreatePlatformDomainResponse]{
		Msg: &domainv1.CreatePlatformDomainResponse{
			PlatformDomain: &domainv1.PlatformDomain{
				Id:        result.ID,
				Domain:    result.Domain,
				IsActive:  result.IsActive,
				CreatedAt: timestamppb.New(result.CreatedAt.Time),
				UpdatedAt: timestamppb.New(result.CreatedAt.Time),
			},
		},
	}, nil
}

// GetPlatformDomain retrieves a platform domain by ID
func (s *DomainServer) GetPlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.GetPlatformDomainRequest],
) (*connect.Response[domainv1.GetPlatformDomainResponse], error) {
	r := req.Msg

	if r.Id <= 0 {
		slog.ErrorContext(ctx, "invalid request: id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	result, err := s.queries.GetPlatformDomain(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get platform domain", "id", r.Id, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}

	return &connect.Response[domainv1.GetPlatformDomainResponse]{
		Msg: &domainv1.GetPlatformDomainResponse{
			PlatformDomain: &domainv1.PlatformDomain{
				Id:        result.ID,
				Domain:    result.Domain,
				IsActive:  result.IsActive,
				CreatedAt: timestamppb.New(result.CreatedAt.Time),
				UpdatedAt: timestamppb.New(result.CreatedAt.Time),
			},
		},
	}, nil
}

// GetPlatformDomainByName retrieves a platform domain by domain name
func (s *DomainServer) GetPlatformDomainByName(
	ctx context.Context,
	req *connect.Request[domainv1.GetPlatformDomainByNameRequest],
) (*connect.Response[domainv1.GetPlatformDomainByNameResponse], error) {
	r := req.Msg

	if r.Domain == "" {
		slog.ErrorContext(ctx, "invalid request: domain is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain is required"))
	}

	result, err := s.queries.GetPlatformDomainByName(ctx, r.Domain)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get platform domain by name", "domain", r.Domain, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrPlatformDomainNotFound)
	}

	return &connect.Response[domainv1.GetPlatformDomainByNameResponse]{
		Msg: &domainv1.GetPlatformDomainByNameResponse{
			PlatformDomain: &domainv1.PlatformDomain{
				Id:        result.ID,
				Domain:    result.Domain,
				IsActive:  result.IsActive,
				CreatedAt: timestamppb.New(result.CreatedAt.Time),
				UpdatedAt: timestamppb.New(result.CreatedAt.Time),
			},
		},
	}, nil
}

// ListActivePlatformDomains lists all active platform domains
func (s *DomainServer) ListActivePlatformDomains(
	ctx context.Context,
	req *connect.Request[domainv1.ListActivePlatformDomainsRequest],
) (*connect.Response[domainv1.ListActivePlatformDomainsResponse], error) {
	results, err := s.queries.ListActivePlatformDomains(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active platform domains", "error", err)
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

	return &connect.Response[domainv1.ListActivePlatformDomainsResponse]{
		Msg: &domainv1.ListActivePlatformDomainsResponse{
			PlatformDomains: domains,
		},
	}, nil
}

// DeactivatePlatformDomain deactivates a platform domain
func (s *DomainServer) DeactivatePlatformDomain(
	ctx context.Context,
	req *connect.Request[domainv1.DeactivatePlatformDomainRequest],
) (*connect.Response[domainv1.DeactivatePlatformDomainResponse], error) {
	r := req.Msg

	if r.Id <= 0 {
		slog.ErrorContext(ctx, "invalid request: id is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	result, err := s.queries.DeactivatePlatformDomain(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to deactivate platform domain", "id", r.Id, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to deactivate platform domain: %w", err))
	}

	return &connect.Response[domainv1.DeactivatePlatformDomainResponse]{
		Msg: &domainv1.DeactivatePlatformDomainResponse{
			PlatformDomain: &domainv1.PlatformDomain{
				Id:        result.ID,
				Domain:    result.Domain,
				IsActive:  result.IsActive,
				CreatedAt: timestamppb.New(result.CreatedAt.Time),
				UpdatedAt: timestamppb.New(result.CreatedAt.Time),
			},
		},
	}, nil
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

// ListAllLocoOwnedDomains lists all loco-owned (subdomain) domains
func (s *DomainServer) ListAllLocoOwnedDomains(
	ctx context.Context,
	req *connect.Request[domainv1.ListAllLocoOwnedDomainsRequest],
) (*connect.Response[domainv1.ListAllLocoOwnedDomainsResponse], error) {
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
			ResourceName:        result.ResourceName,
			ResourceId:          result.ResourceID,
			PlatformDomain: result.PlatformDomain,
		}
	}

	return &connect.Response[domainv1.ListAllLocoOwnedDomainsResponse]{
		Msg: &domainv1.ListAllLocoOwnedDomainsResponse{
			Domains: domains,
		},
	}, nil
}

// AddResourceDomain adds a new domain to an app
func (s *DomainServer) AddResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.AddResourceDomainRequest],
) (*connect.Response[domainv1.AddResourceDomainResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// verify app exists and user has access
	workspaceID, err := s.queries.GetResourceWorkspaceID(ctx, r.ResourceId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("app not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
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

	// check if this is the first domain for the app
	count, err := s.queries.GetResourceDomainCount(ctx, r.ResourceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	resourceDomain, err := s.queries.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
		ResourceID:            r.ResourceId,
		Domain:           fullDomain,
		DomainSource:     domainSource,
		SubdomainLabel:   subdomainLabel,
		PlatformDomainID: platformDomainID,
		IsPrimary:        count == 0, // first domain is primary
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return &connect.Response[domainv1.AddResourceDomainResponse]{
		Msg: &domainv1.AddResourceDomainResponse{
			Domain:  resourceDomainToProto(resourceDomain),
			Message: "Domain added successfully",
		},
	}, nil
}

// UpdateResourceDomain updates a domain for an app
func (s *DomainServer) UpdateResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.UpdateResourceDomainRequest],
) (*connect.Response[domainv1.UpdateResourceDomainResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// get the domain to check its app
	domainRow, err := s.queries.GetResourceDomainByID(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found"))
	}

	// verify user has access to this app
	workspaceID, err := s.queries.GetResourceWorkspaceID(ctx, domainRow.ResourceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	// check if new domain is available (unless it's the same domain)
	if r.Domain != domainRow.Domain {
		available, err := s.queries.CheckDomainAvailability(ctx, r.Domain)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		if !available {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainAlreadyExists)
		}
	}

	// update the domain
	resourceDomain, err := s.queries.UpdateResourceDomain(ctx, genDb.UpdateResourceDomainParams{
		ID:     r.DomainId,
		Domain: r.Domain,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update app domain", "id", r.DomainId, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return &connect.Response[domainv1.UpdateResourceDomainResponse]{
		Msg: &domainv1.UpdateResourceDomainResponse{
			Domain:  resourceDomainToProto(resourceDomain),
			Message: "Domain updated successfully",
		},
	}, nil
}

// SetPrimaryResourceDomain sets which domain is primary for an app
func (s *DomainServer) SetPrimaryResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.SetPrimaryResourceDomainRequest],
) (*connect.Response[domainv1.SetPrimaryResourceDomainResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// verify app exists and user has access
	workspaceID, err := s.queries.GetResourceWorkspaceID(ctx, r.ResourceId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("app not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	// unset primary on all other domains
	err = s.queries.UpdateResourceDomainPrimary(ctx, r.ResourceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// set this domain as primary
	resourceDomain, err := s.queries.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
		ID:    r.DomainId,
		ResourceID: r.ResourceId,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found or does not belong to app"))
	}

	return &connect.Response[domainv1.SetPrimaryResourceDomainResponse]{
		Msg: &domainv1.SetPrimaryResourceDomainResponse{
			Domain:  resourceDomainToProto(resourceDomain),
			Message: "Primary domain updated successfully",
		},
	}, nil
}

// RemoveResourceDomain removes a domain from an app
func (s *DomainServer) RemoveResourceDomain(
	ctx context.Context,
	req *connect.Request[domainv1.RemoveResourceDomainRequest],
) (*connect.Response[domainv1.RemoveResourceDomainResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// get the domain to check its app and whether it's primary
	domainRow, err := s.queries.GetResourceDomainByID(ctx, r.DomainId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("domain not found"))
	}

	// verify user has access to this app
	workspaceID, err := s.queries.GetResourceWorkspaceID(ctx, domainRow.ResourceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
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

	return &connect.Response[domainv1.RemoveResourceDomainResponse]{
		Msg: &domainv1.RemoveResourceDomainResponse{
			Message: "Domain removed successfully",
		},
	}, nil
}
