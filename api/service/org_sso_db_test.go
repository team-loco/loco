package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

type fakeSSO struct {
	connections map[string][]string
	created     int
	failDomains bool
}

func (f *fakeSSO) CreateSAMLConnection(
	_ context.Context,
	metadataURL, metadataXML string,
	domains []string,
) (string, error) {
	if metadataURL == "" && metadataXML == "" {
		return "", &auth.ProviderError{Message: "metadata is required"}
	}
	f.created++
	id := "conn-" + string(rune('0'+f.created))
	f.connections[id] = slices.Clone(domains)
	return id, nil
}

func (f *fakeSSO) SetSAMLDomains(_ context.Context, connectionID string, domains []string) error {
	if f.failDomains {
		return errors.New("provider down")
	}
	f.connections[connectionID] = slices.Clone(domains)
	return nil
}

func (f *fakeSSO) DeleteSAMLConnection(_ context.Context, connectionID string) error {
	delete(f.connections, connectionID)
	return nil
}

func (*fakeSSO) LoginConnection([]auth.AuthMethod) *string {
	return nil
}

func (*fakeSSO) ServiceProvider() (string, string) {
	return "https://auth.test/sso/saml/metadata", "https://auth.test/sso/saml/acs"
}

func withSSO(ctx context.Context, connection string) context.Context {
	return context.WithValue(ctx, contextkeys.SSOConnectionKey, connection)
}

func TestOrgSSOUnavailableWithoutAdmin(t *testing.T) {
	f := newDomainFixture(t)
	got, err := f.orgs.GetOrgSSO(f.owner, connect.NewRequest(&orgv1.GetOrgSSORequest{OrgId: f.orgID}))
	if err != nil || got.Msg.GetAvailable() || got.Msg.GetSso() != nil {
		t.Fatalf("get = %+v (%v)", got, err)
	}
	f.addVerified(t, corpDomain)
	_, err = f.orgs.ConfigureOrgSSO(f.owner, connect.NewRequest(&orgv1.ConfigureOrgSSORequest{
		OrgId: f.orgID, Metadata: &orgv1.ConfigureOrgSSORequest_MetadataXml{MetadataXml: "<x/>"},
	}))
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("configure without an admin: %v", err)
	}
}

const secondDomain = "corp.example"

type ssoFixture struct {
	*domainFixture
	sso        *fakeSSO
	connection string
	admin      context.Context
}

func xmlMetadata(orgID string) *connect.Request[orgv1.ConfigureOrgSSORequest] {
	return connect.NewRequest(&orgv1.ConfigureOrgSSORequest{
		OrgId: orgID, Metadata: &orgv1.ConfigureOrgSSORequest_MetadataXml{MetadataXml: "<x/>"},
	})
}

func newSSOFixture(t *testing.T) *ssoFixture {
	t.Helper()
	f := &ssoFixture{domainFixture: newDomainFixture(t), sso: &fakeSSO{connections: map[string][]string{}}}
	f.orgs.UseSSO(f.sso, "https://auth.test")
	f.addVerified(t, corpDomain)
	configured, err := f.orgs.ConfigureOrgSSO(f.owner, xmlMetadata(f.orgID))
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	f.connection = configured.Msg.GetSso().GetConnectionId()
	f.admin = withSSO(f.owner, f.connection)
	return f
}

func (f *ssoFixture) providerDomains(t *testing.T, want ...string) {
	t.Helper()
	if got := f.sso.connections[f.connection]; !slices.Equal(got, want) {
		t.Fatalf("provider domains = %v, want %v", got, want)
	}
}

func TestOrgSSOConfigure(t *testing.T) {
	f := newDomainFixture(t)
	sso := &fakeSSO{connections: map[string][]string{}}
	f.orgs.UseSSO(sso, "https://auth.test")

	if _, err := f.orgs.ConfigureOrgSSO(f.owner, xmlMetadata(f.orgID)); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("configure without a verified domain: %v", err)
	}
	f.addVerified(t, corpDomain)
	if _, err := f.orgs.ConfigureOrgSSO(f.owner, connect.NewRequest(&orgv1.ConfigureOrgSSORequest{
		OrgId: f.orgID, Metadata: &orgv1.ConfigureOrgSSORequest_MetadataXml{},
	})); codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("provider rejection: %v", err)
	}
	configured, err := f.orgs.ConfigureOrgSSO(f.owner, xmlMetadata(f.orgID))
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if got := sso.connections[configured.Msg.GetSso().GetConnectionId()]; !slices.Equal(got, []string{corpDomain}) {
		t.Fatalf("provider domains = %v", got)
	}
	if _, againErr := f.orgs.ConfigureOrgSSO(
		f.owner,
		xmlMetadata(f.orgID),
	); codeOf(
		againErr,
	) != connect.CodeAlreadyExists {
		t.Fatalf("second configure: %v", againErr)
	}
}

func TestOrgSSORequireNeedsSSOSession(t *testing.T) {
	f := newSSOFixture(t)
	require := connect.NewRequest(&orgv1.SetOrgRequireSSORequest{OrgId: f.orgID, RequireSso: true})
	if _, err := f.orgs.SetOrgRequireSSO(f.owner, require); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("require without signing in through sso: %v", err)
	}
	if _, err := f.orgs.SetOrgRequireSSO(
		withSSO(f.owner, "conn-other"),
		require,
	); codeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("require through another connection: %v", err)
	}
	required, err := f.orgs.SetOrgRequireSSO(f.admin, require)
	if err != nil || !required.Msg.GetSso().GetRequireSso() {
		t.Fatalf("require: %+v (%v)", required, err)
	}
	got, err := f.orgs.GetOrgSSO(f.admin, connect.NewRequest(&orgv1.GetOrgSSORequest{OrgId: f.orgID}))
	if err != nil || !got.Msg.GetAvailable() || !got.Msg.GetSignedInWithSso() || !got.Msg.GetSso().GetRequireSso() ||
		got.Msg.GetServiceProvider().GetAcsUrl() != "https://auth.test/sso/saml/acs" {
		t.Fatalf("get = %+v (%v)", got, err)
	}
	off := connect.NewRequest(&orgv1.SetOrgRequireSSORequest{OrgId: f.orgID, RequireSso: false})
	if _, offErr := f.orgs.SetOrgRequireSSO(f.owner, off); offErr != nil {
		t.Fatalf("turning the requirement off: %v", offErr)
	}
}

func TestOrgSSOFollowsVerifiedDomains(t *testing.T) {
	f := newSSOFixture(t)
	second := f.addVerified(t, secondDomain)
	f.providerDomains(t, secondDomain, corpDomain)

	if _, err := f.orgs.DeleteOrgDomain(f.admin, connect.NewRequest(&orgv1.DeleteOrgDomainRequest{
		OrgId: f.orgID, DomainId: second.GetId(),
	})); err != nil {
		t.Fatalf("delete a domain: %v", err)
	}
	f.providerDomains(t, corpDomain)

	domains, err := f.orgs.ListOrgDomains(f.admin, connect.NewRequest(&orgv1.ListOrgDomainsRequest{OrgId: f.orgID}))
	if err != nil || len(domains.Msg.GetDomains()) != 1 {
		t.Fatalf("domains = %+v (%v)", domains, err)
	}
	if _, deleteErr := f.orgs.DeleteOrgDomain(f.admin, connect.NewRequest(&orgv1.DeleteOrgDomainRequest{
		OrgId: f.orgID, DomainId: domains.Msg.GetDomains()[0].GetId(),
	})); codeOf(deleteErr) != connect.CodeFailedPrecondition {
		t.Fatalf("deleting the last sso domain: %v", deleteErr)
	}

	f.sso.failDomains = true
	pending, err := f.orgs.AddOrgDomain(f.admin, connect.NewRequest(&orgv1.AddOrgDomainRequest{
		OrgId: f.orgID, Domain: "corp.dev",
	}))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	record := pending.Msg.GetDomain()
	f.txt[record.GetVerificationRecordName()] = []string{record.GetVerificationRecordValue()}
	verify := connect.NewRequest(&orgv1.VerifyOrgDomainRequest{OrgId: f.orgID, DomainId: record.GetId()})
	if _, verifyErr := f.orgs.VerifyOrgDomain(f.admin, verify); codeOf(verifyErr) != connect.CodeUnavailable {
		t.Fatalf("verify while the provider is down: %v", verifyErr)
	}
	f.sso.failDomains = false
	if _, verifyErr := f.orgs.VerifyOrgDomain(f.admin, verify); verifyErr != nil {
		t.Fatalf("retry verify: %v", verifyErr)
	}
	f.providerDomains(t, "corp.dev", corpDomain)
}

func TestOrgSSORemoval(t *testing.T) {
	f := newSSOFixture(t)
	if _, err := f.orgs.SetOrgRequireSSO(f.admin, connect.NewRequest(&orgv1.SetOrgRequireSSORequest{
		OrgId: f.orgID, RequireSso: true,
	})); err != nil {
		t.Fatalf("require: %v", err)
	}
	remove := connect.NewRequest(&orgv1.DeleteOrgSSORequest{OrgId: f.orgID})
	if _, err := f.orgs.DeleteOrgSSO(f.admin, remove); err != nil {
		t.Fatalf("delete sso: %v", err)
	}
	if _, ok := f.sso.connections[f.connection]; ok {
		t.Fatal("provider connection survived removal")
	}
	if _, err := f.orgs.DeleteOrgSSO(f.admin, remove); codeOf(err) != connect.CodeNotFound {
		t.Fatalf("second delete: %v", err)
	}

	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{Seq: 0, Limit: 100})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var ssoEvents []string
	for _, e := range all {
		if strings.HasPrefix(e.Type, "org_sso.") {
			ssoEvents = append(ssoEvents, e.Type)
		}
	}
	want := []string{events.OrgSSOConfigured, events.OrgSSORequireChanged, events.OrgSSORemoved}
	if !slices.Equal(ssoEvents, want) {
		t.Fatalf("sso events = %v", ssoEvents)
	}
}

func TestOrgSSORequiresOrgAdmin(t *testing.T) {
	f := newDomainFixture(t)
	f.orgs.UseSSO(&fakeSSO{connections: map[string][]string{}}, "https://auth.test")
	member := f.signUp(t, "member", "member@corp.test", true)
	if err := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID:     member,
		EntityType: genDb.EntityTypeOrganization,
		EntityID:   uuid.MustParse(f.orgID),
		Scope:      genDb.ScopeWrite,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	memberCtx := scoped(t, f.machine, member)
	if _, err := f.orgs.GetOrgSSO(memberCtx, connect.NewRequest(&orgv1.GetOrgSSORequest{
		OrgId: f.orgID,
	})); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("writer read sso: %v", err)
	}
	if _, err := f.orgs.DeleteOrgSSO(memberCtx, connect.NewRequest(&orgv1.DeleteOrgSSORequest{
		OrgId: f.orgID,
	})); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("writer deleted sso: %v", err)
	}
}
