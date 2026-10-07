package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrSSOUnavailable   = errors.New("SAML SSO is not available on this Loco installation")
	ErrSSONotConfigured = errors.New("this organization has no SSO connection")
	ErrSSOConfigured    = errors.New("this organization already has an SSO connection; remove it first")
	ErrSSONeedsDomain   = errors.New("verify at least one domain before connecting SSO")
	ErrSSOSignInFirst   = errors.New("sign in through this organization's SSO before requiring it")
	ErrSSOLastDomain    = errors.New("SSO needs at least one verified domain; remove the SSO connection first")
	ErrSSOProvider      = errors.New("the identity provider could not be reached; try again")
)

func callerSSOConnection(ctx context.Context) string {
	connection, ok := ctx.Value(contextkeys.SSOConnectionKey).(string)
	if !ok {
		return ""
	}
	return connection
}

func orgSSOToProto(row genDb.OrgSso, domains []string) *orgv1.OrgSSO {
	return &orgv1.OrgSSO{
		ConnectionId: row.ConnectionID,
		RequireSso:   row.RequireSso,
		Domains:      domains,
		CreatedAt:    timestamppb.New(row.CreatedAt),
	}
}

func providerConnectError(ctx context.Context, err error) error {
	if rejected, ok := errors.AsType[*auth.ProviderError](err); ok {
		return connect.NewError(connect.CodeInvalidArgument, rejected)
	}
	slog.ErrorContext(ctx, "sso provider call failed", "error", err)
	return connect.NewError(connect.CodeUnavailable, ErrSSOProvider)
}

func (s *OrgServer) loadSSO(ctx context.Context, orgID uuid.UUID) (genDb.OrgSso, bool, error) {
	row, err := s.queries.GetOrgSSO(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to load org sso", "error", err)
		return row, false, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return row, true, nil
}

func (s *OrgServer) verifiedDomains(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	domains, err := s.queries.ListVerifiedOrgDomainNames(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list verified domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if domains == nil {
		domains = []string{}
	}
	return domains, nil
}

func (s *OrgServer) syncSSODomains(ctx context.Context, orgID uuid.UUID, domains []string) error {
	if s.sso == nil {
		return nil
	}
	row, found, err := s.loadSSO(ctx, orgID)
	if err != nil || !found {
		return err
	}
	if syncErr := s.sso.SetSAMLDomains(ctx, row.ConnectionID, domains); syncErr != nil {
		return providerConnectError(ctx, syncErr)
	}
	return nil
}

func (s *OrgServer) GetOrgSSO(
	ctx context.Context,
	req *connect.Request[orgv1.GetOrgSSORequest],
) (*connect.Response[orgv1.GetOrgSSOResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	res := &orgv1.GetOrgSSOResponse{Available: s.sso != nil}
	if s.sso != nil {
		metadataURL, acsURL := s.sso.ServiceProvider()
		res.ServiceProvider = &orgv1.ServiceProvider{MetadataUrl: metadataURL, AcsUrl: acsURL}
	}
	orgID := uuid.MustParse(req.Msg.GetOrgId())
	row, found, err := s.loadSSO(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if found {
		domains, domainsErr := s.verifiedDomains(ctx, orgID)
		if domainsErr != nil {
			return nil, domainsErr
		}
		res.Sso = orgSSOToProto(row, domains)
		res.SignedInWithSso = callerSSOConnection(ctx) == row.ConnectionID
	}
	return connect.NewResponse(res), nil
}

func (s *OrgServer) ConfigureOrgSSO(
	ctx context.Context,
	req *connect.Request[orgv1.ConfigureOrgSSORequest],
) (*connect.Response[orgv1.ConfigureOrgSSOResponse], error) {
	entity, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId())
	if err != nil {
		return nil, err
	}
	if s.sso == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSSOUnavailable)
	}
	orgID := uuid.MustParse(req.Msg.GetOrgId())
	if _, found, loadErr := s.loadSSO(ctx, orgID); loadErr != nil || found {
		if loadErr != nil {
			return nil, loadErr
		}
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrSSOConfigured)
	}
	domains, err := s.verifiedDomains(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSSONeedsDomain)
	}
	connectionID, err := s.sso.CreateSAMLConnection(ctx, req.Msg.GetMetadataUrl(), req.Msg.GetMetadataXml(), domains)
	if err != nil {
		return nil, providerConnectError(ctx, err)
	}
	var createdBy *uuid.UUID
	if entity.Type == genDb.EntityTypeUser {
		createdBy = new(entity.ID)
	}
	row, err := s.queries.CreateOrgSSO(ctx, genDb.CreateOrgSSOParams{
		OrgID:        orgID,
		ConnectionID: connectionID,
		Issuer:       s.ssoIssuer,
		CreatedBy:    createdBy,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to store org sso", "error", err)
		if deleteErr := s.sso.DeleteSAMLConnection(ctx, connectionID); deleteErr != nil {
			slog.ErrorContext(
				ctx,
				"failed to remove orphaned sso connection",
				"connectionId",
				connectionID,
				"error",
				deleteErr,
			)
		}
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrSSOConfigured)
		}
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.OrgSSOConfigured,
		OrgID:       new(orgID),
		SubjectType: events.SubjectSSOConnection,
		Data:        map[string]any{events.FieldConnection: connectionID, "domains": domains},
	})
	return connect.NewResponse(&orgv1.ConfigureOrgSSOResponse{Sso: orgSSOToProto(row, domains)}), nil
}

func (s *OrgServer) SetOrgRequireSSO(
	ctx context.Context,
	req *connect.Request[orgv1.SetOrgRequireSSORequest],
) (*connect.Response[orgv1.SetOrgRequireSSOResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	orgID := uuid.MustParse(req.Msg.GetOrgId())
	row, found, err := s.loadSSO(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSSONotConfigured)
	}
	if req.Msg.GetRequireSso() && callerSSOConnection(ctx) != row.ConnectionID {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSSOSignInFirst)
	}
	updated, err := s.queries.SetOrgRequireSSO(ctx, genDb.SetOrgRequireSSOParams{
		OrgID:      orgID,
		RequireSso: req.Msg.GetRequireSso(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	domains, err := s.verifiedDomains(ctx, orgID)
	if err != nil {
		return nil, err
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.OrgSSORequireChanged,
		OrgID:       new(orgID),
		SubjectType: events.SubjectSSOConnection,
		Data:        map[string]any{events.FieldConnection: row.ConnectionID, "requireSso": updated.RequireSso},
	})
	return connect.NewResponse(&orgv1.SetOrgRequireSSOResponse{Sso: orgSSOToProto(updated, domains)}), nil
}

func (s *OrgServer) DeleteOrgSSO(
	ctx context.Context,
	req *connect.Request[orgv1.DeleteOrgSSORequest],
) (*connect.Response[orgv1.DeleteOrgSSOResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	orgID := uuid.MustParse(req.Msg.GetOrgId())
	row, found, err := s.loadSSO(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, connect.NewError(connect.CodeNotFound, ErrSSONotConfigured)
	}
	if s.sso != nil {
		if deleteErr := s.sso.DeleteSAMLConnection(ctx, row.ConnectionID); deleteErr != nil {
			return nil, providerConnectError(ctx, deleteErr)
		}
	}
	if _, err := s.queries.DeleteOrgSSO(ctx, orgID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.OrgSSORemoved,
		OrgID:       new(orgID),
		SubjectType: events.SubjectSSOConnection,
		Data:        map[string]any{events.FieldConnection: row.ConnectionID},
	})
	return connect.NewResponse(&orgv1.DeleteOrgSSOResponse{}), nil
}

func (s *OrgServer) syncVerifiedDomains(ctx context.Context, orgID uuid.UUID) error {
	domains, err := s.verifiedDomains(ctx, orgID)
	if err != nil {
		return err
	}
	return s.syncSSODomains(ctx, orgID, domains)
}

func (s *OrgServer) releaseSSODomain(ctx context.Context, domain genDb.OrgDomain) error {
	if _, found, err := s.loadSSO(ctx, domain.OrgID); err != nil || !found {
		return err
	}
	domains, err := s.verifiedDomains(ctx, domain.OrgID)
	if err != nil {
		return err
	}
	remaining := slices.DeleteFunc(domains, func(d string) bool { return d == domain.Domain })
	if len(remaining) == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, ErrSSOLastDomain)
	}
	return s.syncSSODomains(ctx, domain.OrgID, remaining)
}
