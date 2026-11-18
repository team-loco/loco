package apps

import (
	"testing"

	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestSignedInCLIListsItsSession(t *testing.T) {
	t.Parallel()
	list, err := p.User.User.ListSessions(t.Context(), &userv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range list.GetSessions() {
		if session.GetCurrent() && session.GetKind() == userv1.SessionKind_SESSION_KIND_CLI {
			return
		}
	}
	t.Fatal("signed-in CLI session is missing from the list")
}
