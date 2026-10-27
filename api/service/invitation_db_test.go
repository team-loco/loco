package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/mail"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	invitationTestTTL = 48 * time.Hour
	invitationWebURL  = "https://app.loco.test"
	invitationEmail   = "bob@acme.test"
	invitationLink    = invitationWebURL + "/login?login_hint=bob%40acme.test"
)

var errInvitationTransportDown = errors.New("smtp relay refused the message")

type fakeTransport struct {
	mu   sync.Mutex
	err  error
	sent []mail.Message
}

func (f *fakeTransport) Send(_ context.Context, msg mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return f.err
}

func (f *fakeTransport) messages() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.Message(nil), f.sent...)
}

func newInvitationOrg(t *testing.T, transport mail.Transport) *membershipOrg {
	t.Helper()
	return newMembershipOrgWith(
		t,
		InvitationConfig{Transport: transport, TTL: invitationTestTTL, WebURL: invitationWebURL},
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

func TestInviteDeliversAndRecordsOutcome(t *testing.T) {
	transport := &fakeTransport{}
	o := newInvitationOrg(t, transport)

	invitation := o.invite(t, "Bob@Acme.test")

	if invitation.GetStatus() != orgv1.InvitationStatus_INVITATION_STATUS_EMAILED {
		t.Fatalf("status = %v", invitation.GetStatus())
	}
	if invitation.GetEmail() != invitationEmail || invitation.GetLink() != invitationLink {
		t.Fatalf("email = %q link = %q", invitation.GetEmail(), invitation.GetLink())
	}
	if invitation.GetInvitedBy().GetId() != o.owner.id.String() {
		t.Fatalf("invited by %q", invitation.GetInvitedBy().GetId())
	}
	row, err := o.api.queries.GetInvitation(t.Context(), uuid.MustParse(invitation.GetId()))
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	if row.DeliveredAt == nil || row.DeliveryError != nil {
		t.Fatalf("delivered_at = %v delivery_error = %v", row.DeliveredAt, row.DeliveryError)
	}
	if ttl := time.Until(row.ExpiresAt); ttl < invitationTestTTL-time.Minute || ttl > invitationTestTTL {
		t.Fatalf("expires in %v", ttl)
	}
	sent := transport.messages()
	if len(sent) != 1 || sent[0].To != invitationEmail || sent[0].ReplyTo != "owner@acme.test" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestInviteDeliveryFailureKeepsTheInvitation(t *testing.T) {
	transport := &fakeTransport{err: errInvitationTransportDown}
	o := newInvitationOrg(t, transport)

	invitation := o.invite(t, invitationEmail)

	if invitation.GetStatus() != orgv1.InvitationStatus_INVITATION_STATUS_FAILED {
		t.Fatalf("status = %v", invitation.GetStatus())
	}
	if invitation.GetDeliveryError() != errInvitationTransportDown.Error() || invitation.GetLink() != invitationLink {
		t.Fatalf("error = %q link = %q", invitation.GetDeliveryError(), invitation.GetLink())
	}
	row, err := o.api.queries.GetInvitation(t.Context(), uuid.MustParse(invitation.GetId()))
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	if row.DeliveredAt != nil || row.DeliveryError == nil {
		t.Fatalf("delivered_at = %v delivery_error = %v", row.DeliveredAt, row.DeliveryError)
	}
}

func TestInviteWithoutTransportReturnsTheLink(t *testing.T) {
	transport, err := mail.ParseTransport("", "", "")
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	o := newInvitationOrg(t, transport)

	invitation := o.invite(t, invitationEmail)

	if invitation.GetStatus() != orgv1.InvitationStatus_INVITATION_STATUS_NO_TRANSPORT {
		t.Fatalf("status = %v", invitation.GetStatus())
	}
	if invitation.GetLink() != invitationLink || invitation.GetDeliveryError() != "" {
		t.Fatalf("link = %q error = %q", invitation.GetLink(), invitation.GetDeliveryError())
	}
	row, err := o.api.queries.GetInvitation(t.Context(), uuid.MustParse(invitation.GetId()))
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	if row.DeliveredAt != nil || row.DeliveryError != nil {
		t.Fatalf("delivered_at = %v delivery_error = %v", row.DeliveredAt, row.DeliveryError)
	}
}

func TestInviteRejections(t *testing.T) {
	o := newInvitationOrg(t, &fakeTransport{})
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
	o := newInvitationOrg(t, &fakeTransport{})
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
		if _, callErr := user.clients.Org.ResendInvitation(ctx, &orgv1.ResendInvitationRequest{
			Id: invitation.GetId(),
		}); codeOf(callErr) != connect.CodePermissionDenied {
			t.Errorf("%s resent: %v", name, callErr)
		}
		if _, callErr := user.clients.Org.RevokeInvitation(ctx, &orgv1.RevokeInvitationRequest{
			Id: invitation.GetId(),
		}); codeOf(callErr) != connect.CodePermissionDenied {
			t.Errorf("%s revoked: %v", name, callErr)
		}
	}
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
	o := newInvitationOrg(t, &fakeTransport{})
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
	o := newInvitationOrg(t, &fakeTransport{})
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

func TestResendExtendsExpiryAndSendsAgain(t *testing.T) {
	transport := &fakeTransport{err: errInvitationTransportDown}
	o := newInvitationOrg(t, transport)
	invitation := o.invite(t, invitationEmail)
	id := uuid.MustParse(invitation.GetId())
	if _, err := o.api.pool.Exec(t.Context(),
		"UPDATE invitations SET expires_at = NOW() - interval '1 hour' WHERE id = $1", id); err != nil {
		t.Fatalf("expire: %v", err)
	}
	transport.mu.Lock()
	transport.err = nil
	transport.mu.Unlock()

	resent, err := o.owner.clients.Org.ResendInvitation(t.Context(), &orgv1.ResendInvitationRequest{Id: id.String()})
	if err != nil {
		t.Fatalf("resend: %v", err)
	}

	got := resent.GetInvitation()
	if got.GetStatus() != orgv1.InvitationStatus_INVITATION_STATUS_EMAILED || got.GetDeliveryError() != "" {
		t.Fatalf("status = %v error = %q", got.GetStatus(), got.GetDeliveryError())
	}
	if ttl := time.Until(got.GetExpiresAt().AsTime()); ttl < invitationTestTTL-time.Minute || ttl > invitationTestTTL {
		t.Fatalf("expires in %v", ttl)
	}
	if len(transport.messages()) != 2 {
		t.Fatalf("sent %d messages", len(transport.messages()))
	}
}
