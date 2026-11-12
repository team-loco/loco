package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	"github.com/team-loco/loco/api/pkg/webhooksig"
	"github.com/team-loco/loco/api/webhooks"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	invitationTestTTL = 48 * time.Hour
	invitationWebURL  = "https://app.loco.test"
	invitationEmail   = "bob@acme.test"
	invitationLink    = invitationWebURL + "/login?login_hint=bob%40acme.test"
)

func newInvitationOrg(t *testing.T) *membershipOrg {
	t.Helper()
	return newMembershipOrgWith(
		t,
		InvitationConfig{TTL: invitationTestTTL, WebURL: invitationWebURL},
	)
}

func (o *membershipOrg) invite(t *testing.T, email string) *orgv1.Invitation {
	t.Helper()
	resp, err := o.owner.clients.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
		OrgId: o.orgID, Email: email,
	})
	if err != nil {
		t.Fatalf("invite %s: %v", email, err)
	}
	return resp.GetInvitation()
}

func TestInvitationEventsCarryEmailDetails(t *testing.T) {
	o := newInvitationOrg(t)
	const inviterName = "Invitation Owner"
	if _, err := o.api.pool.Exec(
		t.Context(),
		"UPDATE users SET name = $2 WHERE id = $1",
		o.owner.id,
		inviterName,
	); err != nil {
		t.Fatal(err)
	}
	secret, secretErr := webhooksig.NewSecret()
	if secretErr != nil {
		t.Fatal(secretErr)
	}
	if err := webhooks.SyncInstallWebhooks(
		t.Context(),
		o.api.pool,
		newTestKeyProvider(t, "k1"),
		[]webhooks.InstallWebhook{
			{URL: installURL, Secret: secret, EventTypes: []string{events.InvitationCreated, events.InvitationResent}},
		},
	); err != nil {
		t.Fatal(err)
	}

	invitation := o.invite(t, "Bob@Acme.test")
	check := func(eventType string, invitation *orgv1.Invitation) {
		t.Helper()
		var raw []byte
		var status string
		var attempts int
		row := o.api.pool.QueryRow(t.Context(), `SELECT e.data, d.status, d.attempts
			FROM events e JOIN webhook_deliveries d ON d.event_seq = e.seq
			WHERE e.type = $1 AND e.subject_id = $2`, eventType, uuid.MustParse(invitation.GetId()))
		if err := row.Scan(&raw, &status, &attempts); err != nil {
			t.Fatal(err)
		}
		var data map[string]string
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			events.FieldEmail:        invitationEmail,
			events.FieldOrgName:      membershipOrgName,
			events.FieldInviterName:  inviterName,
			events.FieldInviterEmail: "owner@acme.test",
			events.FieldExpiresAt:    invitation.GetExpiresAt().AsTime().UTC().Format(time.RFC3339),
			events.FieldLink:         invitationLink,
		}
		if len(data) != len(want) {
			t.Fatalf("data = %v", data)
		}
		for field, value := range want {
			if data[field] != value {
				t.Errorf("%s = %q, want %q", field, data[field], value)
			}
		}
		if status != "pending" || attempts != 0 {
			t.Fatalf("delivery = %s, attempts = %d", status, attempts)
		}
		if invitation.GetLink() != invitationLink || invitation.GetInvitedBy().GetId() != o.owner.id.String() {
			t.Fatalf("invitation = %v", invitation)
		}
	}
	check(events.InvitationCreated, invitation)
	o.expire(t, invitation)
	resent, err := o.owner.clients.Org.ResendInvitation(
		t.Context(),
		&orgv1.ResendInvitationRequest{Id: invitation.GetId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	check(events.InvitationResent, resent.GetInvitation())
	if !resent.GetInvitation().GetExpiresAt().AsTime().After(invitation.GetExpiresAt().AsTime()) {
		t.Fatal("resend did not extend expiry")
	}
}

func TestInviteRejections(t *testing.T) {
	o := newInvitationOrg(t)
	o.invite(t, invitationEmail)
	tests := []struct {
		name  string
		email string
		want  connect.Code
	}{
		{name: "existing member", email: "Owner@acme.test", want: connect.CodeAlreadyExists},
		{name: "duplicate open", email: "BOB@acme.test", want: connect.CodeAlreadyExists},
		{name: "display name", email: "Bob <bob2@acme.test>", want: connect.CodeInvalidArgument},
		{name: "no domain", email: "bob", want: connect.CodeInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := o.owner.clients.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
				OrgId: o.orgID, Email: tt.email,
			})
			if codeOf(err) != tt.want {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestInvitationAuthorization(t *testing.T) {
	o := newInvitationOrg(t)
	member := o.api.signUp(t, "member")
	o.addOrgMember(t, member)
	other := o.api.signUp(t, "other")
	otherOrg, err := other.clients.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new("other-org")})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	invitation := o.invite(t, invitationEmail)
	ctx := t.Context()

	for name, user := range map[string]membershipUser{"member": member, "other admin": other} {
		if _, callErr := user.clients.Org.InviteOrgMember(ctx, &orgv1.InviteOrgMemberRequest{
			OrgId: o.orgID, Email: "carol@acme.test",
		}); codeOf(callErr) != connect.CodePermissionDenied {
			t.Errorf("%s invited: %v", name, callErr)
		}
	}
	if _, err = member.clients.Org.ResendInvitation(ctx, &orgv1.ResendInvitationRequest{
		Id: invitation.GetId(),
	}); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member resent: %v", err)
	}
	if _, err = member.clients.Org.RevokeInvitation(ctx, &orgv1.RevokeInvitationRequest{
		Id: invitation.GetId(),
	}); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member revoked: %v", err)
	}
	resend := func(id string) error {
		_, callErr := other.clients.Org.ResendInvitation(ctx, &orgv1.ResendInvitationRequest{Id: id})
		return callErr
	}
	wantSameNotFound(t, resend(invitation.GetId()), resend(uuid.NewString()))
	revoke := func(id string) error {
		_, callErr := other.clients.Org.RevokeInvitation(ctx, &orgv1.RevokeInvitationRequest{Id: id})
		return callErr
	}
	wantSameNotFound(t, revoke(invitation.GetId()), revoke(uuid.NewString()))
	if _, err = other.clients.Org.ListInvitations(ctx, &orgv1.ListInvitationsRequest{
		OrgId: o.orgID,
	}); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("other admin listed: %v", err)
	}
	if _, err = other.clients.Org.InviteOrgMember(ctx, &orgv1.InviteOrgMemberRequest{
		OrgId: otherOrg.GetOrgId(), Email: "carol@acme.test",
	}); err != nil {
		t.Errorf("other admin invited to their own org: %v", err)
	}

	listed, err := member.clients.Org.ListInvitations(ctx, &orgv1.ListInvitationsRequest{OrgId: o.orgID})
	if err != nil {
		t.Fatalf("member list: %v", err)
	}
	if len(listed.GetInvitations()) != 1 || listed.GetInvitations()[0].GetId() != invitation.GetId() {
		t.Fatalf("listed = %v", listed.GetInvitations())
	}
}

func TestRevokedInvitationLeavesTheList(t *testing.T) {
	o := newInvitationOrg(t)
	invitation := o.invite(t, invitationEmail)

	if _, err := o.owner.clients.Org.RevokeInvitation(t.Context(), &orgv1.RevokeInvitationRequest{
		Id: invitation.GetId(),
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	listed, err := o.owner.clients.Org.ListInvitations(t.Context(), &orgv1.ListInvitationsRequest{OrgId: o.orgID})
	if err != nil || len(listed.GetInvitations()) != 0 {
		t.Fatalf("listed = %v, err = %v", listed.GetInvitations(), err)
	}
	_, err = o.owner.clients.Org.RevokeInvitation(t.Context(), &orgv1.RevokeInvitationRequest{Id: invitation.GetId()})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second revoke: %v", err)
	}
	_, err = o.owner.clients.Org.ResendInvitation(t.Context(), &orgv1.ResendInvitationRequest{Id: invitation.GetId()})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("resend after revoke: %v", err)
	}
	o.invite(t, invitationEmail)
}

func TestClaimedInvitationCannotBeRevokedOrResent(t *testing.T) {
	o := newInvitationOrg(t)
	invitation := o.invite(t, invitationEmail)
	o.api.signUp(t, "bob")

	_, err := o.owner.clients.Org.RevokeInvitation(t.Context(), &orgv1.RevokeInvitationRequest{Id: invitation.GetId()})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("revoke claimed: %v", err)
	}
	_, err = o.owner.clients.Org.ResendInvitation(t.Context(), &orgv1.ResendInvitationRequest{Id: invitation.GetId()})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("resend claimed: %v", err)
	}
	_, err = o.owner.clients.Org.InviteOrgMember(t.Context(), &orgv1.InviteOrgMemberRequest{
		OrgId: o.orgID, Email: invitationEmail,
	})
	if codeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("invite a member: %v", err)
	}
}

func (o *membershipOrg) expire(t *testing.T, invitation *orgv1.Invitation) {
	t.Helper()
	if _, err := o.api.pool.Exec(t.Context(),
		"UPDATE invitations SET expires_at = NOW() - interval '1 hour' WHERE id = $1",
		uuid.MustParse(invitation.GetId())); err != nil {
		t.Fatalf("expire: %v", err)
	}
}

func TestListShowsAnExpiredInvitationAsExpired(t *testing.T) {
	o := newInvitationOrg(t)
	invitation := o.invite(t, invitationEmail)
	o.expire(t, invitation)

	listed, err := o.owner.clients.Org.ListInvitations(t.Context(), &orgv1.ListInvitationsRequest{OrgId: o.orgID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := listed.GetInvitations()
	if len(got) != 1 || got[0].GetId() != invitation.GetId() {
		t.Fatalf("listed = %v, want the expired invitation", got)
	}

	if !got[0].GetExpiresAt().AsTime().Before(time.Now()) {
		t.Fatalf("expires at %v, want a past time", got[0].GetExpiresAt().AsTime())
	}
}

func TestInviteAgainReplacesAnExpiredInvitation(t *testing.T) {
	o := newInvitationOrg(t)
	expired := o.invite(t, invitationEmail)
	o.expire(t, expired)

	renewed := o.invite(t, "Bob@Acme.test")

	if renewed.GetId() == expired.GetId() {
		t.Fatal("invite reused the expired invitation")
	}

	ttl := time.Until(renewed.GetExpiresAt().AsTime())
	if ttl < invitationTestTTL-time.Minute || ttl > invitationTestTTL {
		t.Fatalf("expires in %v", ttl)
	}
	old, err := o.api.queries.GetInvitation(t.Context(), uuid.MustParse(expired.GetId()))
	if err != nil {
		t.Fatalf("old row: %v", err)
	}
	if old.RevokedAt == nil {
		t.Fatal("expired invitation still open")
	}
	listed, err := o.owner.clients.Org.ListInvitations(t.Context(), &orgv1.ListInvitationsRequest{OrgId: o.orgID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.GetInvitations()) != 1 || listed.GetInvitations()[0].GetId() != renewed.GetId() {
		t.Fatalf("listed = %v, want only the new invitation", listed.GetInvitations())
	}
}

func TestConcurrentInvitesOverAnExpiredInvitationCreateOne(t *testing.T) {
	o := newInvitationOrg(t)
	o.expire(t, o.invite(t, invitationEmail))

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, errs[i] = o.owner.clients.Org.InviteOrgMember(context.Background(), &orgv1.InviteOrgMemberRequest{
				OrgId: o.orgID, Email: invitationEmail,
			})
		})
	}
	wg.Wait()

	created := 0
	for i, err := range errs {
		if err == nil {
			created++
			continue
		}
		if codeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("invite %d: %v", i, err)
		}
	}
	if created != 1 {
		t.Fatalf("created %d invitations, want 1", created)
	}
	var open int
	if err := o.api.pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM invitations WHERE claimed_at IS NULL AND revoked_at IS NULL").Scan(&open); err != nil {
		t.Fatalf("count: %v", err)
	}
	if open != 1 {
		t.Fatalf("open invitations = %d, want 1", open)
	}
}
