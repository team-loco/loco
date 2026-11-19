package service

import "testing"

func TestNewUserBelongsToNoOrganization(t *testing.T) {
	t.Parallel()
	api := newMembershipAPI(t)
	user := api.signUp(t, "fresh.user")
	if ids := userOrgIDs(t, user); len(ids) != 0 {
		t.Fatalf("organizations of a new user = %v, want none", ids)
	}
}
