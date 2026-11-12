package service

import (
	"context"
	"errors"
	"fmt"
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
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
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
	TTL    time.Duration
	WebURL string
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

	var result *orgv1.Invitation
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
		var recordErr error
		result, recordErr = s.recordInvitationEvent(ctx, qtx, created, events.InvitationCreated)
		return recordErr
	})
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
	var result *orgv1.Invitation
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		extended, extendErr := qtx.ExtendInvitation(ctx, genDb.ExtendInvitationParams{
			ID: invitation.ID, ExpiresAt: time.Now().Add(s.invitations.TTL),
		})
		if errors.Is(extendErr, pgx.ErrNoRows) {
			return ErrInvitationNotOpen
		}
		if extendErr != nil {
			return fmt.Errorf("extend invitation: %w", extendErr)
		}
		var recordErr error
		result, recordErr = s.recordInvitationEvent(ctx, qtx, extended, events.InvitationResent)
		return recordErr
	})
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

func (s *OrgServer) recordInvitationEvent(
	ctx context.Context, qtx *genDb.Queries, invitation genDb.Invitation, eventType string,
) (*orgv1.Invitation, error) {
	org, err := qtx.GetOrgByID(ctx, invitation.OrgID)
	if err != nil {
		return nil, fmt.Errorf("get invitation organization: %w", err)
	}
	inviter, err := qtx.GetUserByID(ctx, invitation.InvitedBy)
	if err != nil {
		return nil, fmt.Errorf("get inviter: %w", err)
	}
	result := s.invitationProto(invitation, inviter)
	if err := events.Record(ctx, qtx, events.Event{
		Type:        eventType,
		OrgID:       new(invitation.OrgID),
		SubjectType: events.SubjectInvitation,
		SubjectID:   new(invitation.ID),
		Data: map[string]any{
			events.FieldEmail:        invitation.Email,
			events.FieldOrgName:      org.Name,
			events.FieldInviterName:  derefString(inviter.Name),
			events.FieldInviterEmail: inviter.Email,
			events.FieldExpiresAt:    invitation.ExpiresAt.UTC().Format(time.RFC3339),
			events.FieldLink:         result.GetLink(),
		},
	}); err != nil {
		return nil, err
	}
	return result, nil
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
		CreatedAt: timestamppb.New(invitation.CreatedAt),
		ExpiresAt: timestamppb.New(invitation.ExpiresAt),
		Link:      s.invitations.link(invitation.Email),
	}
}
