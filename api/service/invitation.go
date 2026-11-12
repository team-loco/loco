package service

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/mail"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/clip"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	maxDeliveryErrorLength = 500
	deliveryRecordTimeout  = 5 * time.Second
)

var (
	ErrInvalidInviteEmail = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"email must be a plain address like name@example.com",
	)
	ErrAlreadyOrgMember = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"that email already belongs to a member of the organization",
	)
	ErrInvitationExists = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"that email already has an open invitation to the organization",
	)
	ErrInvitationNotFound = rpcerr.NewCoded(connect.CodeNotFound, "invitation not found")
	ErrInvitationClaimed  = rpcerr.NewCoded(connect.CodeFailedPrecondition, "invitation was already claimed")
	ErrInvitationNotOpen  = rpcerr.NewCoded(connect.CodeFailedPrecondition, "invitation is no longer open")
	ErrInviterMustBeUser  = rpcerr.NewCoded(connect.CodePermissionDenied, "only users can send invitations")
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
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.InviteOrgMember, orgID)); err != nil {
		return nil, err
	}
	if p.Entity.Type != genDb.EntityTypeUser {
		return nil, ErrInviterMustBeUser
	}
	address, err := netmail.ParseAddress(req.GetEmail())
	if err != nil || address.Name != "" || address.Address != strings.TrimSpace(req.GetEmail()) {
		return nil, ErrInvalidInviteEmail
	}
	email := strings.ToLower(address.Address)

	var invitation genDb.Invitation
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		isMember, memberErr := qtx.IsOrgMemberEmail(ctx, genDb.IsOrgMemberEmailParams{OrgID: orgID, Email: email})
		if memberErr != nil {
			return fmt.Errorf("check organization membership: %w", memberErr)
		}
		if isMember {
			return ErrAlreadyOrgMember
		}
		if revokeErr := revokeExpiredInvitations(ctx, qtx, orgID, email); revokeErr != nil {
			return revokeErr
		}
		created, createErr := qtx.CreateInvitation(ctx, genDb.CreateInvitationParams{
			OrgID:     orgID,
			Email:     email,
			InvitedBy: p.Entity.ID,
			ExpiresAt: time.Now().Add(s.invitations.TTL),
		})
		if isUniqueViolation(createErr, pgOpenInvitationUnique) {
			return ErrInvitationExists
		}
		if createErr != nil {
			return fmt.Errorf("insert invitation: %w", createErr)
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
	if err != nil {
		return nil, err
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
		return fmt.Errorf("revoke expired invitations: %w", err)
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
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListInvitations, orgID); err != nil {
		return nil, err
	}
	pageSize := normalizePageSize(req.GetPageSize())
	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}
	rows, err := s.queries.ListOrgInvitations(ctx, genDb.ListOrgInvitationsParams{
		OrgID:         orgID,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	inviters := map[uuid.UUID]genDb.User{}
	invitations := make([]*orgv1.Invitation, len(rows))
	for i, row := range rows {
		inviter, known := inviters[row.InvitedBy]
		if !known {
			inviter, err = s.queries.GetUserByID(ctx, row.InvitedBy)
			if err != nil {
				return nil, fmt.Errorf("get inviter: %w", err)
			}
			inviters[row.InvitedBy] = inviter
		}
		invitations[i] = s.invitationProto(row, inviter)
	}
	token := nextPageToken(rows, pageSize, func(i genDb.Invitation) (uuid.UUID, time.Time) { return i.ID, i.CreatedAt })
	return &orgv1.ListInvitationsResponse{Invitations: invitations, NextPageToken: token}, nil
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
		return nil, ErrInvitationClaimed
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		revoked, revokeErr := qtx.RevokeInvitation(ctx, invitation.ID)
		if errors.Is(revokeErr, pgx.ErrNoRows) {
			return ErrInvitationNotOpen
		}
		if revokeErr != nil {
			return fmt.Errorf("revoke invitation: %w", revokeErr)
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
		return nil, err
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
		return nil, ErrInvitationNotOpen
	}
	if err != nil {
		return nil, fmt.Errorf("extend invitation: %w", err)
	}
	result, err := s.deliverInvitation(ctx, extended)
	if err != nil {
		return nil, err
	}
	return &orgv1.ResendInvitationResponse{Invitation: result}, nil
}

func (s *OrgServer) adminInvitation(ctx context.Context, action actions.Action, id string) (genDb.Invitation, error) {
	invitationID, err := parseID("invitation_id", id)
	if err != nil {
		return genDb.Invitation{}, err
	}
	invitation, err := s.queries.GetInvitation(ctx, invitationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Invitation{}, ErrInvitationNotFound
	}
	if err != nil {
		return genDb.Invitation{}, fmt.Errorf("get invitation: %w", err)
	}
	required := actions.New(action, invitation.OrgID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrInvitationNotFound); authErr != nil {
		return genDb.Invitation{}, authErr
	}
	return invitation, nil
}

func (s *OrgServer) deliverInvitation(ctx context.Context, invitation genDb.Invitation) (*orgv1.Invitation, error) {
	org, err := s.queries.GetOrgByID(ctx, invitation.OrgID)
	if err != nil {
		return nil, fmt.Errorf("get invitation organization: %w", err)
	}
	inviter, err := s.queries.GetUserByID(ctx, invitation.InvitedBy)
	if err != nil {
		return nil, fmt.Errorf("get inviter: %w", err)
	}
	event := mail.InvitationEvent{
		Invitation: mail.InvitationInfo{
			ID: invitation.ID, OrgID: invitation.OrgID, Email: invitation.Email, ExpiresAt: invitation.ExpiresAt,
		},
		Org:     mail.OrgInfo{ID: org.ID, Name: org.Name},
		Inviter: mail.InviterInfo{ID: inviter.ID, Email: inviter.Email, Name: derefString(inviter.Name)},
		Link:    s.invitations.link(invitation.Email),
	}
	sendErr := s.invitations.Transport.Send(ctx, event)
	detached := context.WithoutCancel(ctx)
	recordCtx, cancel := context.WithTimeout(detached, deliveryRecordTimeout)
	defer cancel()
	switch {
	case errors.Is(sendErr, mail.ErrNoTransport):
	case sendErr != nil:
		slog.ErrorContext(ctx, "failed to deliver invitation", "invitationId", invitation.ID, "error", sendErr)
		text := clip.Bytes(sendErr.Error(), maxDeliveryErrorLength)
		err = s.queries.SetInvitationDeliveryError(recordCtx, genDb.SetInvitationDeliveryErrorParams{
			ID: invitation.ID, DeliveryError: &text,
		})
	default:
		err = s.queries.SetInvitationDelivered(recordCtx, invitation.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("record delivery of invitation %s: %w", invitation.ID, err)
	}
	stored, err := s.queries.GetInvitation(recordCtx, invitation.ID)
	if err != nil {
		return nil, fmt.Errorf("reload invitation %s: %w", invitation.ID, err)
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
