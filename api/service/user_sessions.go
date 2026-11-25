package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func (s *UserServer) ListSessions(
	ctx context.Context,
	req *userv1.ListSessionsRequest,
) (*userv1.ListSessionsResponse, error) {
	principal, userID, err := s.sessionOwner(ctx, req.GetUserId(), actions.ListUserSessions)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSessionsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list account sessions: %w", err)
	}

	sessions := make([]*userv1.Session, 0, len(rows))
	for _, row := range rows {
		kind := userv1.SessionKind_SESSION_KIND_CLI
		if row.Kind == genDb.SessionKindWeb {
			kind = userv1.SessionKind_SESSION_KIND_WEB
		}
		session := &userv1.Session{
			Id:         row.ID.String(),
			Kind:       kind,
			CreatedAt:  timestamppb.New(row.CreatedAt),
			LastUsedAt: timestamppb.New(row.LastUsedAt),
			Current:    row.ID == principal.SessionID,
		}
		if row.UserAgent != nil {
			session.UserAgent = *row.UserAgent
		}
		if row.IpAddress != nil {
			session.IpAddress = row.IpAddress.String()
		}
		sessions = append(sessions, session)
	}
	return &userv1.ListSessionsResponse{Sessions: sessions}, nil
}

func (s *UserServer) RevokeSession(
	ctx context.Context,
	req *userv1.RevokeSessionRequest,
) (*userv1.RevokeSessionResponse, error) {
	_, userID, err := s.sessionOwner(ctx, req.GetUserId(), actions.RevokeUserSessions)
	if err != nil {
		return nil, err
	}
	id, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	err = s.queries.DeleteSessionForUser(ctx, genDb.DeleteSessionForUserParams{ID: id, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("revoke account session: %w", err)
	}
	return &userv1.RevokeSessionResponse{}, nil
}

func (s *UserServer) RevokeUserSessions(
	ctx context.Context,
	req *userv1.RevokeUserSessionsRequest,
) (*userv1.RevokeUserSessionsResponse, error) {
	_, userID, err := s.sessionOwner(ctx, req.GetUserId(), actions.RevokeUserSessions)
	if err != nil {
		return nil, err
	}
	if err = s.queries.DeleteSessionsForUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("revoke user sessions: %w", err)
	}
	return &userv1.RevokeUserSessionsResponse{}, nil
}

// sessionOwner returns the caller and the account a session request acts on. The caller's own account
// needs a session sign-in; another account needs action on that user.
func (s *UserServer) sessionOwner(
	ctx context.Context,
	userID string,
	action actions.Action,
) (reqctx.Principal, uuid.UUID, error) {
	principal, err := principalFrom(ctx)
	if err != nil {
		return reqctx.Principal{}, uuid.Nil, err
	}
	if userID != "" {
		target, parseErr := parseID("user_id", userID)
		if parseErr != nil {
			return reqctx.Principal{}, uuid.Nil, parseErr
		}
		if target != principal.UserID {
			return principal, target, check(ctx, s.authz, principal, actions.New(action, target))
		}
	}
	principal, err = requireSession(ctx)
	return principal, principal.UserID, err
}
