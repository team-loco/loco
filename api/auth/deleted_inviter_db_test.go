package auth

import (
	"testing"
	"time"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
)

func TestInvitationCanBeClaimedAfterInviterDeletion(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	q := genDb.New(pool)
	resolver := NewResolver(pool, open(t), limits.Defaults(), time.Hour)
	inviter, _, err := resolver.Resolve(t.Context(), identity("inviter", "inviter@deleted.test", true))
	if err != nil {
		t.Fatal(err)
	}
	org, err := q.CreateOrganization(
		t.Context(),
		genDb.CreateOrganizationParams{Name: "surviving", CreatedBy: inviter.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.CreateInvitation(
		t.Context(),
		genDb.CreateInvitationParams{
			OrgID:     org.ID,
			Email:     "invitee@deleted.test",
			InvitedBy: inviter.ID,
			ExpiresAt: time.Now().Add(time.Hour),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.DeleteUser(t.Context(), inviter.ID); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := resolver.Resolve(t.Context(), identity("invitee", "invitee@deleted.test", true))
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].OrgID != org.ID || claimed[0].InviterName != "Unavailable inviter" {
		t.Fatalf("claimed invitation = %+v", claimed)
	}
	count, err := q.CountOrgsCreatedBy(t.Context(), inviter.ID)
	if err != nil || count != 1 {
		t.Fatalf("historical creator quota = %d, %v", count, err)
	}
}
