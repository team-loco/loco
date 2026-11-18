package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
)

func TestRefreshReportsSignedOutWithoutDashboardConfiguration(t *testing.T) {
	t.Parallel()
	s := &AuthServer{}
	_, err := s.RefreshWebSession(t.Context(), &authv1.RefreshWebSessionRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("refresh without dashboard configuration: %v", err)
	}
}
