package service

import (
	"context"
	"errors"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/mail"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const maxDeliveryErrorLength = 500

var (
	ErrInvalidInviteEmail = errors.New("email must be a plain address like name@example.com")
	ErrAlreadyOrgMember   = errors.New("that email already belongs to a member of the organization")
	ErrInvitationExists   = errors.New("that email already has an open invitation to the organization")
	ErrInvitationNotFound = errors.New("invitation not found")
	ErrInvitationClaimed  = errors.New("invitation was already claimed")
	ErrInvitationNotOpen  = errors.New("invitation is no longer open")
	ErrInviterMustBeUser  = errors.New("only users can send invitations")
)

type InvitationConfig struct {
	Transport mail.Transport
	TTL       time.Duration
	WebURL    string
}

func (c InvitationConfig) link(email string) string {
	return strings.TrimSuffix(c.WebURL, "/") + "/login?login_hint=" + url.QueryEscape(email)
}

func (s *OrgServer) InviteOrgMember(
	ctx context.Context,
	req *orgv1.InviteOrgMemberRequest,
) (*orgv1.InviteOrgMemberResponse, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if err := s.checkOrg(ctx, actions.InviteOrgMember, req.GetOrgId()); err != nil {
		return nil, err
	}
	if entity.Type != genDb.EntityTypeUser {
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrInviterMustBeUser)
	}
	address, err := netmail.ParseAddress(req.GetEmail())
	if err != nil || address.Name != "" || address.Address != strings.TrimSpace(req.GetEmail()) {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrInvalidInviteEmail)
	}
	email := strings.ToLower(address.Address)
	orgID := uuid.MustParse(req.GetOrgId())

	var invitation genDb.Invitation
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		isMember, memberErr := qtx.IsOrgMemberEmail(ctx, genDb.IsOrgMemberEmailParams{OrgID: orgID, Email: email})
		if memberErr != nil {
			return memberErr
		}
		if isMember {
			return rpcerr.New(connect.CodeAlreadyExists, ErrAlreadyOrgMember)
		}
		if revokeErr := revokeExpiredInvitations(ctx, qtx, orgID, email); revokeErr != nil {
			return revokeErr
		}
		created, createErr := qtx.CreateInvitation(ctx, genDb.CreateInvitationParams{
			OrgID:     orgID,
			Email:     email,
			InvitedBy: entity.ID,
			ExpiresAt: time.Now().Add(s.invitations.TTL),
		})
		if createErr != nil {
			return createErr
		}
		invitation = created
		return events.Record(ctx, qtx, events.Event{
			Type:        events.InvitationCreated,
			OrgID:       new(orgID),
			SubjectType: events.SubjectInvitation,
			SubjectID:   new(created.ID),
			Data:        map[string]any{events.FieldEmail: email},
		})
	})
	if isPgConstraintViolation(err) {
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrInvitationExists)
	}
	if err != nil {
		return nil, txError(ctx, "failed to create invitation", err)
	}

	result, err := s.deliverInvitation(ctx, invitation)
	if err != nil {
		return nil, err
	}
	return &orgv1.InviteOrgMemberResponse{Invitation: result}, nil
}

func revokeExpiredInvitations(ctx context.Context, qtx *genDb.Queries, orgID uuid.UUID, email string) error {
	expired, err := qtx.RevokeExpiredInvitations(ctx, genDb.RevokeExpiredInvitationsParams{OrgID: orgID, Email: email})
	if err != nil {
		return err
	}
	for _, invitation := range expired {
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.InvitationRevoked,
			OrgID:       new(invitation.OrgID),
			SubjectType: events.SubjectInvitation,
			SubjectID:   new(invitation.ID),
			Data:        map[string]any{events.FieldEmail: invitation.Email},
		}); eventErr != nil {
			return eventErr
		}
	}
	return nil
}

func (s *OrgServer) ListInvitations(
	ctx context.Context,
	req *orgv1.ListInvitationsRequest,
) (*orgv1.ListInvitationsResponse, error) {
	if err := s.checkOrg(ctx, actions.ListInvitations, req.GetOrgId()); err != nil {
		return nil, err
	}
	pageSize := normalizePageSize(req.GetPageSize())
	var pageToken *uuid.UUID
	if req.GetPageToken() != "" {
		cursorID, err := decodeCursor(req.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
		}
		parsed := uuid.MustParse(cursorID)
		pageToken = &parsed
	}
	rows, err := s.queries.ListOrgInvitations(ctx, genDb.ListOrgInvitationsParams{
		OrgID:     uuid.MustParse(req.GetOrgId()),
		PageToken: pageToken,
		Limit:     pageSize,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list invitations", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	inviters := map[uuid.UUID]genDb.User{}
	invitations := make([]*orgv1.Invitation, len(rows))
	for i, row := range rows {
		inviter, known := inviters[row.InvitedBy]
		if !known {
			inviter, err = s.queries.GetUserByID(ctx, row.InvitedBy)
			if err != nil {
				slog.ErrorContext(ctx, "failed to load inviter", "error", err)
				return nil, rpcerr.New(connect.CodeInternal, ErrDB)
			}
			inviters[row.InvitedBy] = inviter
		}
		invitations[i] = s.invitationProto(row, inviter)
	}
	var nextPageToken string
	if len(rows) == int(pageSize) {
		nextPageToken = encodeCursor(rows[len(rows)-1].ID.String())
	}
	return &orgv1.ListInvitationsResponse{Invitations: invitations, NextPageToken: nextPageToken}, nil
}

func (s *OrgServer) RevokeInvitation(
	ctx context.Context,
	req *orgv1.RevokeInvitationRequest,
) (*orgv1.RevokeInvitationResponse, error) {
	invitation, err := s.adminInvitation(ctx, actions.RevokeInvitation, req.GetId())
	if err != nil {
		return nil, err
	}
	if invitation.ClaimedAt != nil {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrInvitationClaimed)
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		revoked, revokeErr := qtx.RevokeInvitation(ctx, invitation.ID)
		if errors.Is(revokeErr, pgx.ErrNoRows) {
			return rpcerr.New(connect.CodeFailedPrecondition, ErrInvitationNotOpen)
		}
		if revokeErr != nil {
			return revokeErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.InvitationRevoked,
			OrgID:       new(revoked.OrgID),
			SubjectType: events.SubjectInvitation,
			SubjectID:   new(revoked.ID),
			Data:        map[string]any{events.FieldEmail: revoked.Email},
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to revoke invitation", err)
	}
	return &orgv1.RevokeInvitationResponse{}, nil
}

func (s *OrgServer) ResendInvitation(
	ctx context.Context,
	req *orgv1.ResendInvitationRequest,
) (*orgv1.ResendInvitationResponse, error) {
	invitation, err := s.adminInvitation(ctx, actions.ResendInvitation, req.GetId())
	if err != nil {
		return nil, err
	}
	extended, err := s.queries.ExtendInvitation(ctx, genDb.ExtendInvitationParams{
		ID:        invitation.ID,
		ExpiresAt: time.Now().Add(s.invitations.TTL),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrInvitationNotOpen)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to extend invitation", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	result, err := s.deliverInvitation(ctx, extended)
	if err != nil {
		return nil, err
	}
	return &orgv1.ResendInvitationResponse{Invitation: result}, nil
}

func (s *OrgServer) adminInvitation(ctx context.Context, action actions.Action, id string) (genDb.Invitation, error) {
	invitation, err := s.queries.GetInvitation(ctx, uuid.MustParse(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Invitation{}, rpcerr.New(connect.CodeNotFound, ErrInvitationNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to load invitation", "error", err)
		return genDb.Invitation{}, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if checkErr := s.checkOrg(ctx, action, invitation.OrgID.String()); checkErr != nil {
		return genDb.Invitation{}, checkErr
	}
	return invitation, nil
}

func (s *OrgServer) checkOrg(ctx context.Context, action actions.Action, orgID string) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}
	if err := s.authz.Check(ctx, scopes, actions.New(action, orgID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to manage invitations", "orgId", orgID)
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return nil
}

func (s *OrgServer) deliverInvitation(ctx context.Context, invitation genDb.Invitation) (*orgv1.Invitation, error) {
	org, err := s.queries.GetOrgByID(ctx, invitation.OrgID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load invitation org", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	inviter, err := s.queries.GetUserByID(ctx, invitation.InvitedBy)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load inviter", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	message := mail.NewInvitationMessage(mail.InvitationEvent{
		Invitation: mail.InvitationInfo{
			ID: invitation.ID, OrgID: invitation.OrgID, Email: invitation.Email, ExpiresAt: invitation.ExpiresAt,
		},
		Org:     mail.OrgInfo{ID: org.ID, Name: org.Name},
		Inviter: mail.InviterInfo{ID: inviter.ID, Email: inviter.Email, Name: derefString(inviter.Name)},
		Link:    s.invitations.link(invitation.Email),
	})
	sendErr := s.invitations.Transport.Send(ctx, message)
	switch {
	case errors.Is(sendErr, mail.ErrNoTransport):
	case sendErr != nil:
		slog.ErrorContext(ctx, "failed to deliver invitation", "invitationId", invitation.ID, "error", sendErr)
		text := truncate(sendErr.Error(), maxDeliveryErrorLength)
		err = s.queries.SetInvitationDeliveryError(ctx, genDb.SetInvitationDeliveryErrorParams{
			ID: invitation.ID, DeliveryError: &text,
		})
	default:
		err = s.queries.SetInvitationDelivered(ctx, invitation.ID)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to record invitation delivery", "invitationId", invitation.ID, "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	stored, err := s.queries.GetInvitation(ctx, invitation.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to reload invitation", "invitationId", invitation.ID, "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	return s.invitationProto(stored, inviter), nil
}

func (s *OrgServer) invitationProto(invitation genDb.Invitation, inviter genDb.User) *orgv1.Invitation {
	return &orgv1.Invitation{
		Id:    invitation.ID.String(),
		OrgId: invitation.OrgID.String(),
		Email: invitation.Email,
		InvitedBy: &orgv1.User{
			Id: inviter.ID.String(), Email: inviter.Email, Name: derefString(inviter.Name),
			AvatarUrl: derefString(inviter.AvatarUrl),
		},
		CreatedAt:     timestamppb.New(invitation.CreatedAt),
		ExpiresAt:     timestamppb.New(invitation.ExpiresAt),
		Status:        invitationStatus(invitation),
		DeliveryError: derefString(invitation.DeliveryError),
		Link:          s.invitations.link(invitation.Email),
	}
}

func invitationStatus(invitation genDb.Invitation) orgv1.InvitationStatus {
	switch {
	case !invitation.ExpiresAt.After(time.Now()):
		return orgv1.InvitationStatus_INVITATION_STATUS_EXPIRED
	case invitation.DeliveredAt != nil:
		return orgv1.InvitationStatus_INVITATION_STATUS_EMAILED
	case invitation.DeliveryError != nil:
		return orgv1.InvitationStatus_INVITATION_STATUS_FAILED
	}
	return orgv1.InvitationStatus_INVITATION_STATUS_NO_TRANSPORT
}

func truncate(s string, limit int) string {
	if len(s) > limit {
		return s[:limit]
	}
	return s
}
