package service

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func (a *membershipAPI) grantSystem(t *testing.T, user membershipUser, scope genDb.Scope) {
	t.Helper()
	if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: user.id, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: scope,
	}); err != nil {
		t.Fatalf("grant system %s: %v", scope, err)
	}
}

func (a *membershipAPI) setName(t *testing.T, user membershipUser, name string) {
	t.Helper()
	params := genDb.UpdateUserProfileParams{ID: user.id, Name: &name}
	if _, err := a.queries.UpdateUserProfile(t.Context(), params); err != nil {
		t.Fatalf("name user: %v", err)
	}
}

func listedEmails(users []*userv1.User) []string {
	emails := make([]string, 0, len(users))
	for _, user := range users {
		emails = append(emails, user.GetEmail())
	}
	slices.Sort(emails)
	return emails
}

func TestListUsersSearchesNameAndEmailAndCounts(t *testing.T) {
	t.Parallel()
	a := newMembershipAPI(t)
	reader := a.signUp(t, "reader")
	a.grantSystem(t, reader, genDb.ScopeRead)
	ada := a.signUp(t, "ada")
	a.setName(t, ada, "Ada Lovelace")
	grace := a.signUp(t, "grace")
	a.setName(t, grace, "Grace Hopper")
	a.signUp(t, "pager1")
	a.signUp(t, "pager2")
	a.signUp(t, "pager3")
	a.signUp(t, "under_score")
	a.signUp(t, "underXscore")
	ctx := t.Context()

	search := func(query string, pageSize int32) *userv1.ListUsersResponse {
		t.Helper()
		listed, err := reader.clients.User.ListUsers(ctx,
			&userv1.ListUsersRequest{Query: query, PageSize: pageSize, IncludeTotalCount: true})
		if err != nil {
			t.Fatalf("list users %q: %v", query, err)
		}
		return listed
	}
	for query, want := range map[string][]string{
		"LOVELACE": {membershipEmail("ada")},
		"hopper":   {membershipEmail("grace")},
		"GRACE@":   {membershipEmail("grace")},
		"under_":   {membershipEmail("under_score")},
		"nobody":   nil,
	} {
		listed := search(query, 0)
		got := listedEmails(listed.GetUsers())
		if !slices.Equal(got, want) || int(listed.GetTotalCount()) != len(want) {
			t.Errorf("query %q = %v (total %d), want %v", query, got, listed.GetTotalCount(), want)
		}
	}

	var seen []string
	request := &userv1.ListUsersRequest{Query: "PAGER", PageSize: 2, IncludeTotalCount: true}
	for pages := 0; ; pages++ {
		listed, err := reader.clients.User.ListUsers(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if listed.GetTotalCount() != 3 {
			t.Errorf("page %d total = %d, want 3", pages, listed.GetTotalCount())
		}
		seen = append(seen, listedEmails(listed.GetUsers())...)
		if listed.GetNextPageToken() == "" {
			if pages != 1 {
				t.Errorf("last page = %d, want 1", pages)
			}
			break
		}
		request.PageToken = listed.GetNextPageToken()
	}
	slices.Sort(seen)
	wantSeen := []string{membershipEmail("pager1"), membershipEmail("pager2"), membershipEmail("pager3")}
	if !slices.Equal(seen, wantSeen) {
		t.Errorf("paged users = %v, want %v", seen, wantSeen)
	}
	withoutTotal, err := reader.clients.User.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil || withoutTotal.GetTotalCount() != 0 {
		t.Fatalf("list without include_total_count: total %d, err %v", withoutTotal.GetTotalCount(), err)
	}
}

func TestOrdinaryUserCannotListUsers(t *testing.T) {
	t.Parallel()
	a := newMembershipAPI(t)
	ordinary := a.signUp(t, "ordinary")
	_, err := ordinary.clients.User.ListUsers(t.Context(), &userv1.ListUsersRequest{Query: "ordinary"})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("list users as an ordinary user: code %v, want %v", code, connect.CodePermissionDenied)
	}
}

func TestSystemReaderSeesUserStateAndLastActive(t *testing.T) {
	t.Parallel()
	a := newMembershipAPI(t)
	reader := a.signUp(t, "reader")
	a.grantSystem(t, reader, genDb.ScopeRead)
	admin := a.signUp(t, "admin")
	a.grantSystem(t, admin, genDb.ScopeAdmin)
	target := a.signUp(t, "target")
	neverSignedIn, _ := a.signIn(t, "dormant")
	ctx := t.Context()
	older := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	webToken, _, err := a.machine.IssueWebSession(ctx, target.id, netip.MustParseAddr("198.51.100.9"), "browser")
	if err != nil {
		t.Fatal(err)
	}
	for token, usedAt := range map[string]time.Time{target.token: older, webToken: latest} {
		principal, tokenErr := a.machine.GetToken(ctx, token)
		if tokenErr != nil {
			t.Fatal(tokenErr)
		}
		if _, execErr := a.pool.Exec(ctx, `UPDATE session_tokens SET last_used_at = $2 WHERE id = $1`,
			principal.SessionID, usedAt); execErr != nil {
			t.Fatal(execErr)
		}
	}
	if err = setStatus(t, admin, target, userv1.UserStatus_USER_STATUS_SUSPENDED); err != nil {
		t.Fatal(err)
	}

	listed, err := reader.clients.User.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*userv1.User{}
	for _, user := range listed.GetUsers() {
		byID[user.GetId()] = user
	}
	if user := byID[target.id.String()]; user.GetStatus() != userv1.UserStatus_USER_STATUS_SUSPENDED ||
		!user.GetLastActiveAt().AsTime().Equal(latest) {
		t.Errorf(
			"suspended user in list: status %v, last active %v, want %v",
			user.GetStatus(),
			user.GetLastActiveAt(),
			latest,
		)
	}
	if user := byID[reader.id.String()]; user.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE ||
		user.GetLastActiveAt() == nil {
		t.Errorf("active user in list: status %v, last active %v", user.GetStatus(), user.GetLastActiveAt())
	}
	if user := byID[neverSignedIn.ID.String()]; user.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE ||
		user.GetLastActiveAt() != nil {
		t.Errorf("user without a session: status %v, last active %v", user.GetStatus(), user.GetLastActiveAt())
	}

	got, err := reader.clients.User.GetUser(ctx, &userv1.GetUserRequest{
		Key: &userv1.GetUserRequest_UserId{UserId: target.id.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if user := got.GetUser(); user.GetStatus() != userv1.UserStatus_USER_STATUS_SUSPENDED ||
		!user.GetLastActiveAt().AsTime().Equal(latest) {
		t.Errorf("get user: status %v, last active %v, want %v", user.GetStatus(), user.GetLastActiveAt(), latest)
	}
	_, err = reader.clients.User.GetUser(ctx, &userv1.GetUserRequest{
		Key: &userv1.GetUserRequest_UserId{UserId: uuid.NewString()},
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Errorf("get missing user: code %v, want %v", code, connect.CodeNotFound)
	}

	ordinary := a.signUp(t, "ordinary")
	self, err := ordinary.clients.User.GetUser(ctx, &userv1.GetUserRequest{
		Key: &userv1.GetUserRequest_UserId{UserId: ordinary.id.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if user := self.GetUser(); user.GetStatus() != userv1.UserStatus_USER_STATUS_UNSPECIFIED ||
		user.GetLastActiveAt() != nil {
		t.Errorf("ordinary user's own view: status %v, last active %v", user.GetStatus(), user.GetLastActiveAt())
	}
}
