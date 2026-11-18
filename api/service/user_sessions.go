package service

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func (s *UserServer) ListSessions(
	ctx context.Context,
	_ *userv1.ListSessionsRequest,
) (*userv1.ListSessionsResponse, error) {
	principal, err := sessionPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSessionsForUser(ctx, principal.UserID)
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
		sessions = append(sessions, session)
	}
	return &userv1.ListSessionsResponse{Sessions: sessions}, nil
}

func (s *UserServer) RevokeSession(
	ctx context.Context,
	req *userv1.RevokeSessionRequest,
) (*userv1.RevokeSessionResponse, error) {
	principal, err := sessionPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	err = s.queries.DeleteSessionForUser(ctx, genDb.DeleteSessionForUserParams{ID: id, UserID: principal.UserID})
	if err != nil {
		return nil, fmt.Errorf("revoke account session: %w", err)
	}
	return &userv1.RevokeSessionResponse{}, nil
}

func sessionPrincipal(ctx context.Context) (reqctx.Principal, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return reqctx.Principal{}, err
	}
	if p.SessionID == uuid.Nil {
		return reqctx.Principal{}, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied)
	}
	return p, nil
}
