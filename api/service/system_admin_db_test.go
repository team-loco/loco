package service

import (
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/events"
	"github.com/team-loco/loco/api/limits"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
)

const (
	configAdminSubject = "config-admin"
	unclaimedAdmin     = "later@acme.test"
)

// newSystemAdminAPI lists config-admin@acme.test and later@acme.test in the configuration and
// signs in config-admin, who claims its entry.
func newSystemAdminAPI(t *testing.T) (*membershipAPI, membershipUser) {
	t.Helper()
	a := newMembershipAPI(t)
	admins, err := auth.ParseSystemAdmins([]string{membershipEmail(configAdminSubject), unclaimedAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.SyncSystemAdmins(t.Context(), a.pool, admins); err != nil {
		t.Fatal(err)
	}
	a.resolver = auth.NewResolver(a.pool, auth.SignupPolicy{}, limits.Defaults(), auth.WithSystemAdmins(admins))
	admin := a.signUp(t, configAdminSubject)
	return a, admin
}

func listAdmins(t *testing.T, caller membershipUser) *adminv1.ListSystemAdminsResponse {
	t.Helper()
	listed, err := caller.clients.SystemAdmin.ListSystemAdmins(t.Context(), &adminv1.ListSystemAdminsRequest{})
	if err != nil {
		t.Fatalf("list system admins: %v", err)
	}
	return listed
}

func adminSources(listed *adminv1.ListSystemAdminsResponse, userID uuid.UUID) []adminv1.SystemAdminSource {
	for _, admin := range listed.GetAdmins() {
		if admin.GetUserId() == userID.String() {
			return admin.GetSources()
		}
	}
	return nil
}

func grant(t *testing.T, caller, target membershipUser) error {
	t.Helper()
	_, err := caller.clients.SystemAdmin.GrantSystemAdmin(t.Context(),
		&adminv1.GrantSystemAdminRequest{UserId: target.id.String()})
	return err
}

func revoke(t *testing.T, caller, target membershipUser) error {
	t.Helper()
	_, err := caller.clients.SystemAdmin.RevokeSystemAdmin(t.Context(),
		&adminv1.RevokeSystemAdminRequest{UserId: target.id.String()})
	return err
}

func TestListSystemAdminsShowsSourcesAndUnclaimedEmails(t *testing.T) {
	a, configAdmin := newSystemAdminAPI(t)
	granted := a.signUp(t, "granted")
	if err := grant(t, configAdmin, granted); err != nil {
		t.Fatalf("grant: %v", err)
	}

	listed := listAdmins(t, configAdmin)
	config := adminv1.SystemAdminSource_SYSTEM_ADMIN_SOURCE_CONFIG
	grantSource := adminv1.SystemAdminSource_SYSTEM_ADMIN_SOURCE_GRANT
	if got := adminSources(listed, configAdmin.id); !slices.Equal(got, []adminv1.SystemAdminSource{config}) {
		t.Fatalf("config admin sources = %v", got)
	}
	if got := adminSources(listed, granted.id); !slices.Equal(got, []adminv1.SystemAdminSource{grantSource}) {
		t.Fatalf("granted admin sources = %v", got)
	}
	for _, admin := range listed.GetAdmins() {
		if admin.GetUserId() == configAdmin.id.String() &&
			(admin.GetEmail() != membershipEmail(configAdminSubject) || admin.GetIssuer() != testIssuer ||
				admin.GetSubject() != configAdminSubject) {
			t.Fatalf("config admin = %+v", admin)
		}
	}
	if got := listed.GetUnclaimedEmails(); !slices.Equal(got, []string{unclaimedAdmin}) {
		t.Fatalf("unclaimed emails = %v, want [%s]", got, unclaimedAdmin)
	}
}

func TestGrantAndRevokeSystemAdmin(t *testing.T) {
	a, configAdmin := newSystemAdminAPI(t)
	target := a.signUp(t, "target")

	if err := grant(t, configAdmin, target); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if code := connect.CodeOf(grant(t, configAdmin, target)); code != connect.CodeAlreadyExists {
		t.Fatalf("second grant code = %v, want %v", code, connect.CodeAlreadyExists)
	}
	if n := a.countEvents(t, events.SystemAdminGranted, target.id); n != 1 {
		t.Fatalf("system_admin.granted events = %d, want 1", n)
	}
	if _, err := target.clients.SystemAdmin.ListSystemAdmins(
		t.Context(), &adminv1.ListSystemAdminsRequest{},
	); err != nil {
		t.Fatalf("granted user lists admins: %v", err)
	}

	if err := revoke(t, target, target); err != nil {
		t.Fatalf("revoke own grant: %v", err)
	}
	if n := a.countEvents(t, events.SystemAdminRevoked, target.id); n != 1 {
		t.Fatalf("system_admin.revoked events = %d, want 1", n)
	}
	if code := connect.CodeOf(grant(t, target, configAdmin)); code != connect.CodePermissionDenied {
		t.Fatalf("revoked user grant code = %v, want %v", code, connect.CodePermissionDenied)
	}
	if code := connect.CodeOf(revoke(t, configAdmin, target)); code != connect.CodeNotFound {
		t.Fatalf("revoke a non-admin code = %v, want %v", code, connect.CodeNotFound)
	}
	unknown := membershipUser{id: uuid.New()}
	if code := connect.CodeOf(grant(t, configAdmin, unknown)); code != connect.CodeNotFound {
		t.Fatalf("grant an unknown user code = %v, want %v", code, connect.CodeNotFound)
	}
}

func TestRevokeSystemAdminRefusesConfigAdminsAndTheLastAdmin(t *testing.T) {
	a, configAdmin := newSystemAdminAPI(t)
	other := a.signUp(t, "other")
	if err := grant(t, configAdmin, other); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if code := connect.CodeOf(revoke(t, other, configAdmin)); code != connect.CodeFailedPrecondition {
		t.Fatalf("revoke a config admin code = %v, want %v", code, connect.CodeFailedPrecondition)
	}

	if err := auth.SyncSystemAdmins(t.Context(), a.pool, nil); err != nil {
		t.Fatalf("remove config admins: %v", err)
	}
	if code := connect.CodeOf(revoke(t, other, other)); code != connect.CodeFailedPrecondition {
		t.Fatalf("revoke the last admin code = %v, want %v", code, connect.CodeFailedPrecondition)
	}
	if got := adminSources(listAdmins(t, other), other.id); len(got) != 1 {
		t.Fatalf("last admin sources = %v, want the grant", got)
	}
}

func TestConfigRemovalKeepsAGrantedAdmin(t *testing.T) {
	a, configAdmin := newSystemAdminAPI(t)
	other := a.signUp(t, "other")
	if err := grant(t, other, other); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("self grant err = %v, want permission denied", err)
	}
	if err := grant(t, configAdmin, configAdmin); err != nil {
		t.Fatalf("grant the config admin: %v", err)
	}
	if err := auth.SyncSystemAdmins(t.Context(), a.pool, nil); err != nil {
		t.Fatalf("remove config admins: %v", err)
	}
	got := adminSources(listAdmins(t, configAdmin), configAdmin.id)
	if !slices.Equal(got, []adminv1.SystemAdminSource{adminv1.SystemAdminSource_SYSTEM_ADMIN_SOURCE_GRANT}) {
		t.Fatalf("sources after config removal = %v, want the grant only", got)
	}
}
