package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestCreateUserAndFirstSignIn(t *testing.T) {
	a := newMembershipAPI(t)
	admin := a.signUp(t, "sysadmin")
	request := &userv1.CreateUserRequest{Email: "Invited@acme.test"}
	if _, err := admin.clients.User.CreateUser(
		t.Context(),
		request,
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("non-admin create: %v", err)
	}
	if err := a.queries.AddUserScope(
		t.Context(),
		genDb.AddUserScopeParams{
			UserID:     admin.id,
			EntityType: genDb.EntityTypeSystem,
			EntityID:   uuid.Nil,
			Scope:      genDb.ScopeAdmin,
		},
	); err != nil {
		t.Fatal(err)
	}
	response, err := admin.clients.User.CreateUser(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	user := response.GetUser()
	userID := uuid.MustParse(user.GetId())
	if user.GetEmail() != "invited@acme.test" {
		t.Fatalf("email = %q", user.GetEmail())
	}
	identities, err := a.queries.ListIdentitiesForUser(t.Context(), userID)
	if err != nil || len(identities) != 0 {
		t.Fatalf("identities before sign-in = %v, %v", identities, err)
	}
	if count := a.countEvents(t, events.UserCreated, userID); count != 1 {
		t.Fatalf("user.created events = %d", count)
	}
	for _, email := range []string{request.GetEmail(), user.GetEmail()} {
		if _, err := admin.clients.User.CreateUser(
			t.Context(),
			&userv1.CreateUserRequest{Email: email},
		); connect.CodeOf(
			err,
		) != connect.CodeAlreadyExists {
			t.Fatalf("duplicate %s: %v", email, err)
		}
	}
	for _, email := range []string{"", "missing-at-sign", "Name <valid@example.test>"} {
		if _, err := admin.clients.User.CreateUser(
			t.Context(),
			&userv1.CreateUserRequest{Email: email},
		); connect.CodeOf(
			err,
		) != connect.CodeInvalidArgument {
			t.Fatalf("invalid email %q: %v", email, err)
		}
	}
	a.resolver = auth.NewResolver(a.pool, auth.SignupPolicy{Mode: auth.SignupClosed})
	invited := a.signUp(t, "invited")
	if _, err := invited.clients.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{}); err != nil {
		t.Fatalf("self scopes: %v", err)
	}
	var orgs int
	if err := a.pool.QueryRow(t.Context(), "SELECT count(*) FROM organizations WHERE created_by = $1", userID).
		Scan(&orgs); err != nil ||
		orgs != 0 {
		t.Fatalf("organizations before onboarding = %d, %v", orgs, err)
	}
	if _, err := invited.clients.Org.CreateOrg(
		t.Context(),
		&orgv1.CreateOrgRequest{Name: new("invited-org")},
	); err != nil {
		t.Fatalf("normal organization onboarding: %v", err)
	}
}
