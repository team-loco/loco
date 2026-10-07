package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm/actions"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const verificationPrefix = "_loco-verification."

var (
	ErrOrgDomainNotFound   = errors.New("domain not found in this organization")
	ErrDomainInvalid       = errors.New("enter a domain with at least two labels, like example.com")
	ErrDomainExists        = errors.New("this organization already added that domain")
	ErrDomainClaimed       = errors.New("another organization already verified that domain")
	ErrDomainNotVerified   = errors.New("verify the domain before turning on auto-join")
	ErrDomainRecordMissing = errors.New(
		"the verification TXT record was not found yet; DNS changes can take a few minutes",
	)
)

type TXTLookup func(ctx context.Context, name string) ([]string, error)

func normalizeDomain(raw string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.Contains(d, "..") {
		return "", ErrDomainInvalid
	}
	return d, nil
}

func scopeFromProto(s tokenv1.Scope) (genDb.Scope, bool) {
	switch s {
	case tokenv1.Scope_SCOPE_READ:
		return genDb.ScopeRead, true
	case tokenv1.Scope_SCOPE_WRITE:
		return genDb.ScopeWrite, true
	case tokenv1.Scope_SCOPE_ADMIN:
		return genDb.ScopeAdmin, true
	case tokenv1.Scope_SCOPE_UNSPECIFIED:
		return "", false
	default:
		return "", false
	}
}

func scopeToProto(s *string) tokenv1.Scope {
	if s == nil {
		return tokenv1.Scope_SCOPE_UNSPECIFIED
	}
	switch *s {
	case genDb.ScopeRead:
		return tokenv1.Scope_SCOPE_READ
	case genDb.ScopeWrite:
		return tokenv1.Scope_SCOPE_WRITE
	case genDb.ScopeAdmin:
		return tokenv1.Scope_SCOPE_ADMIN
	default:
		return tokenv1.Scope_SCOPE_UNSPECIFIED
	}
}

func orgDomainToProto(d genDb.OrgDomain) *orgv1.OrgDomain {
	out := &orgv1.OrgDomain{
		Id:                      d.ID.String(),
		Domain:                  d.Domain,
		Verified:                d.VerifiedAt != nil,
		VerificationRecordName:  verificationPrefix + d.Domain,
		VerificationRecordValue: "loco-verification=" + d.VerificationToken,
		AutoJoinScope:           scopeToProto(d.AutoJoinScope),
		CreatedAt:               timestamppb.New(d.CreatedAt),
	}
	if d.VerifiedAt != nil {
		out.VerifiedAt = timestamppb.New(*d.VerifiedAt)
	}
	return out
}

func (s *OrgServer) requireOrgAdmin(ctx context.Context, orgID string) (genDb.Entity, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return genDb.Entity{}, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return genDb.Entity{}, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.ManageOrgDomains, orgID),
	); err != nil {
		return genDb.Entity{}, connect.NewError(connect.CodePermissionDenied, err)
	}
	return entity, nil
}

func (s *OrgServer) AddOrgDomain(
	ctx context.Context,
	req *connect.Request[orgv1.AddOrgDomainRequest],
) (*connect.Response[orgv1.AddOrgDomainResponse], error) {
	entity, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId())
	if err != nil {
		return nil, err
	}
	domain, err := normalizeDomain(req.Msg.GetDomain())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	token, err := randomToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	var createdBy *uuid.UUID
	if entity.Type == genDb.EntityTypeUser {
		createdBy = new(entity.ID)
	}
	orgID := uuid.MustParse(req.Msg.GetOrgId())
	row, err := s.queries.CreateOrgDomain(ctx, genDb.CreateOrgDomainParams{
		OrgID:             orgID,
		Domain:            domain,
		VerificationToken: token,
		CreatedBy:         createdBy,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainExists)
		}
		slog.ErrorContext(ctx, "failed to add org domain", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.OrgDomainAdded,
		OrgID:       new(orgID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(row.ID),
		Data:        map[string]any{events.FieldDomain: domain},
	})
	return connect.NewResponse(&orgv1.AddOrgDomainResponse{Domain: orgDomainToProto(row)}), nil
}

func (s *OrgServer) ListOrgDomains(
	ctx context.Context,
	req *connect.Request[orgv1.ListOrgDomainsRequest],
) (*connect.Response[orgv1.ListOrgDomainsResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListOrgDomains(ctx, uuid.MustParse(req.Msg.GetOrgId()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	out := make([]*orgv1.OrgDomain, len(rows))
	for i, row := range rows {
		out[i] = orgDomainToProto(row)
	}
	return connect.NewResponse(&orgv1.ListOrgDomainsResponse{Domains: out}), nil
}

func (s *OrgServer) loadDomain(ctx context.Context, orgID, domainID string) (genDb.OrgDomain, error) {
	row, err := s.queries.GetOrgDomain(ctx, genDb.GetOrgDomainParams{
		ID:    uuid.MustParse(domainID),
		OrgID: uuid.MustParse(orgID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, connect.NewError(connect.CodeNotFound, ErrOrgDomainNotFound)
	}
	if err != nil {
		return row, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return row, nil
}

func (s *OrgServer) VerifyOrgDomain(
	ctx context.Context,
	req *connect.Request[orgv1.VerifyOrgDomainRequest],
) (*connect.Response[orgv1.VerifyOrgDomainResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	row, err := s.loadDomain(ctx, req.Msg.GetOrgId(), req.Msg.GetDomainId())
	if err != nil {
		return nil, err
	}
	if row.VerifiedAt != nil {
		if syncErr := s.syncVerifiedDomains(ctx, row.OrgID); syncErr != nil {
			return nil, syncErr
		}
		return connect.NewResponse(&orgv1.VerifyOrgDomainResponse{Domain: orgDomainToProto(row)}), nil
	}
	records, lookupErr := s.lookupTXT(ctx, verificationPrefix+row.Domain)
	want := "loco-verification=" + row.VerificationToken
	found := false
	for _, r := range records {
		if strings.TrimSpace(r) == want {
			found = true
			break
		}
	}
	if !found {
		slog.InfoContext(ctx, "domain verification record not found", "domain", row.Domain, "error", lookupErr)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrDomainRecordMissing)
	}
	verified, err := s.queries.MarkOrgDomainVerified(
		ctx,
		genDb.MarkOrgDomainVerifiedParams{ID: row.ID, OrgID: row.OrgID},
	)
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrDomainClaimed)
		}
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.OrgDomainVerified,
		OrgID:       new(row.OrgID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(row.ID),
		Data:        map[string]any{events.FieldDomain: row.Domain},
	})
	if syncErr := s.syncVerifiedDomains(ctx, row.OrgID); syncErr != nil {
		return nil, syncErr
	}
	return connect.NewResponse(&orgv1.VerifyOrgDomainResponse{Domain: orgDomainToProto(verified)}), nil
}

func (s *OrgServer) SetOrgDomainAutoJoin(
	ctx context.Context,
	req *connect.Request[orgv1.SetOrgDomainAutoJoinRequest],
) (*connect.Response[orgv1.SetOrgDomainAutoJoinResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	row, err := s.loadDomain(ctx, req.Msg.GetOrgId(), req.Msg.GetDomainId())
	if err != nil {
		return nil, err
	}
	var scope *string
	if parsed, ok := scopeFromProto(req.Msg.GetScope()); ok {
		if row.VerifiedAt == nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrDomainNotVerified)
		}
		scopes, scopesOK := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
		if !scopesOK {
			return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
		}
		if verifyErr := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, genDb.EntityScope{
			EntityType: genDb.EntityTypeOrganization, EntityID: row.OrgID, Scope: parsed,
		}); verifyErr != nil {
			return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
		}
		scope = &parsed
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "failed to roll back auto-join change", "error", rbErr)
		}
	}()
	qtx := genDb.New(tx)
	updated, err := qtx.SetOrgDomainAutoJoin(ctx, genDb.SetOrgDomainAutoJoinParams{
		AutoJoinScope: scope, ID: row.ID, OrgID: row.OrgID,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	added := 0
	if scope != nil {
		added, err = auth.AutoJoinDomain(ctx, qtx, row.OrgID, row.Domain, *scope)
		if err != nil {
			slog.ErrorContext(ctx, "failed to add existing users on domain", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
	}
	if err := events.RecordWith(ctx, qtx, events.Event{
		Type:        events.OrgDomainAutoJoin,
		OrgID:       new(row.OrgID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(row.ID),
		Data:        map[string]any{events.FieldDomain: row.Domain, "scope": derefString(scope), "usersAdded": added},
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return connect.NewResponse(&orgv1.SetOrgDomainAutoJoinResponse{
		Domain:     orgDomainToProto(updated),
		UsersAdded: int32(min(added, int(^uint32(0)>>1))),
	}), nil
}

func (s *OrgServer) DeleteOrgDomain(
	ctx context.Context,
	req *connect.Request[orgv1.DeleteOrgDomainRequest],
) (*connect.Response[orgv1.DeleteOrgDomainResponse], error) {
	if _, err := s.requireOrgAdmin(ctx, req.Msg.GetOrgId()); err != nil {
		return nil, err
	}
	row, err := s.loadDomain(ctx, req.Msg.GetOrgId(), req.Msg.GetDomainId())
	if err != nil {
		return nil, err
	}
	if row.VerifiedAt != nil {
		if ssoErr := s.releaseSSODomain(ctx, row); ssoErr != nil {
			return nil, ssoErr
		}
	}
	if _, err := s.queries.DeleteOrgDomain(ctx, genDb.DeleteOrgDomainParams{ID: row.ID, OrgID: row.OrgID}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("%w: %w", ErrDB, err))
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.OrgDomainRemoved,
		OrgID:       new(row.OrgID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(row.ID),
		Data:        map[string]any{events.FieldDomain: row.Domain},
	})
	return connect.NewResponse(&orgv1.DeleteOrgDomainResponse{}), nil
}
