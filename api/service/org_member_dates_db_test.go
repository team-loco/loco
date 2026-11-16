package service

import (
	"testing"
	"time"

	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func TestOrgMemberDates(t *testing.T) {
	t.Parallel()
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	joined := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	login := joined.Add(time.Hour)
	if _, err := o.api.pool.Exec(
		t.Context(),
		"UPDATE user_scopes SET created_at=$1 WHERE user_id=$2",
		joined,
		member.id,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := o.api.pool.Exec(
		t.Context(),
		"UPDATE identities SET last_login_at=$1 WHERE user_id=$2",
		login,
		member.id,
	); err != nil {
		t.Fatal(err)
	}
	read := func() *orgv1.OrgMember {
		t.Helper()
		response, err := o.owner.clients.Org.ListOrgMembers(t.Context(), &orgv1.ListOrgMembersRequest{OrgId: o.orgID})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range response.GetMembers() {
			if row.GetUser().GetId() == member.id.String() {
				return row
			}
		}
		t.Fatal("member missing")
		return nil
	}
	for _, role := range []orgv1.OrgRole{
		orgv1.OrgRole_ORG_ROLE_MEMBER,
		orgv1.OrgRole_ORG_ROLE_ADMIN,
		orgv1.OrgRole_ORG_ROLE_MEMBER,
	} {
		if _, err := o.owner.clients.Org.SetOrgMemberRole(
			t.Context(),
			&orgv1.SetOrgMemberRoleRequest{OrgId: o.orgID, UserId: member.id.String(), Role: role},
		); err != nil {
			t.Fatal(err)
		}
		row := read()
		if !row.GetJoinedAt().AsTime().Equal(joined) || !row.GetLastLoginAt().AsTime().Equal(login) {
			t.Fatalf("member dates = %v", row)
		}
	}
	before := time.Now().Add(-time.Second)
	o.api.signIn(t, member.subject)
	if row := read(); row.GetLastLoginAt().AsTime().Before(before) || !row.GetJoinedAt().AsTime().Equal(joined) {
		t.Fatalf("sign-in dates = %v", row)
	}
}
