package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

type fakeSSO struct {
	mu          sync.Mutex
	connections map[string][]string
	created     int
	failDomains bool
	failDelete  bool
	paused      chan struct{}
	resume      chan struct{}
}

func (f *fakeSSO) CreateSAMLConnection(
	_ context.Context,
	metadataURL, metadataXML string,
	domains []string,
) (string, error) {
	if metadataURL == "" && metadataXML == "" {
		return "", &auth.ProviderError{Message: "metadata is required"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	id := "conn-" + string(rune('0'+f.created))
	f.connections[id] = slices.Clone(domains)
	return id, nil
}

func (f *fakeSSO) SetSAMLDomains(_ context.Context, connectionID string, domains []string) error {
	f.mu.Lock()
	paused := f.paused
	f.paused = nil
	f.mu.Unlock()
	if paused != nil {
		close(paused)
		<-f.resume
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDomains {
		return errProviderDown
	}
	f.connections[connectionID] = slices.Clone(domains)
	return nil
}

func (f *fakeSSO) DeleteSAMLConnection(_ context.Context, connectionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete {
		return errProviderDown
	}
	delete(f.connections, connectionID)
	return nil
}

func (f *fakeSSO) connection(id string) ([]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	domains, ok := f.connections[id]
	return domains, ok
}

func (f *fakeSSO) pauseNextDomainSync() (paused, resume chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = make(chan struct{})
	f.resume = make(chan struct{})
	return f.paused, f.resume
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

const (
	secondDomain     = "corp.example"
	lockPollInterval = 5 * time.Millisecond
)

var errProviderDown = errors.New("provider down")

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
	if got, _ := f.sso.connection(f.connection); !slices.Equal(got, want) {
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
	if got, _ := sso.connection(configured.Msg.GetSso().GetConnectionId()); !slices.Equal(got, []string{corpDomain}) {
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

	f.sso.mu.Lock()
	f.sso.failDomains = true
	f.sso.mu.Unlock()
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
	f.sso.mu.Lock()
	f.sso.failDomains = false
	f.sso.mu.Unlock()
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
	if _, ok := f.sso.connection(f.connection); ok {
		t.Fatal("provider connection survived removal")
	}
	if _, err := f.orgs.DeleteOrgSSO(f.admin, remove); codeOf(err) != connect.CodeNotFound {
		t.Fatalf("second delete: %v", err)
	}

	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 100})
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

func TestOrgSSOChangesFailWithoutTheirEvent(t *testing.T) {
	f := newSSOFixture(t)
	if _, err := f.pool.Exec(t.Context(), failEventInsertsSQL); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}
	require := connect.NewRequest(&orgv1.SetOrgRequireSSORequest{OrgId: f.orgID, RequireSso: true})
	if _, err := f.orgs.SetOrgRequireSSO(f.admin, require); err == nil {
		t.Fatal("require sso succeeded without its event")
	}
	got, err := f.orgs.GetOrgSSO(f.admin, connect.NewRequest(&orgv1.GetOrgSSORequest{OrgId: f.orgID}))
	if err != nil || got.Msg.GetSso().GetRequireSso() {
		t.Fatalf("sso after failed require = %+v (%v)", got, err)
	}
}

func TestOrgSSODeleteOrgRemovesTheConnection(t *testing.T) {
	f := newSSOFixture(t)
	deleteOrg := connect.NewRequest(&orgv1.DeleteOrgRequest{OrgId: f.orgID})
	orgID := uuid.MustParse(f.orgID)

	f.sso.mu.Lock()
	f.sso.failDelete = true
	f.sso.mu.Unlock()
	if _, err := f.orgs.DeleteOrg(f.admin, deleteOrg); codeOf(err) != connect.CodeUnavailable {
		t.Fatalf("delete org while the provider is down: %v", err)
	}
	if _, err := f.queries.GetOrgByID(t.Context(), orgID); err != nil {
		t.Fatalf("org removed although its connection could not be: %v", err)
	}
	if _, ok := f.sso.connection(f.connection); !ok {
		t.Fatal("connection removed by a failed delete")
	}

	f.sso.mu.Lock()
	f.sso.failDelete = false
	f.sso.mu.Unlock()
	if _, err := f.orgs.DeleteOrg(f.admin, deleteOrg); err != nil {
		t.Fatalf("delete org: %v", err)
	}
	if _, ok := f.sso.connection(f.connection); ok {
		t.Fatal("provider connection outlived its org")
	}
	if _, err := f.queries.GetOrgByID(t.Context(), orgID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("org after delete: %v", err)
	}
}

func blockedOrDone(t *testing.T, pool *pgxpool.Pool, results <-chan error) []error {
	t.Helper()
	for {
		select {
		case err := <-results:
			return []error{err}
		default:
		}
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
		)`).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if waiting {
			return nil
		}
		time.Sleep(lockPollInterval)
	}
}

func TestOrgSSOConcurrentDomainDeletesKeepOneDomain(t *testing.T) {
	f := newSSOFixture(t)
	second := f.addVerified(t, secondDomain)
	domains, err := f.orgs.ListOrgDomains(f.admin, connect.NewRequest(&orgv1.ListOrgDomainsRequest{OrgId: f.orgID}))
	if err != nil {
		t.Fatalf("domains: %v", err)
	}
	var first string
	for _, d := range domains.Msg.GetDomains() {
		if d.GetDomain() == corpDomain {
			first = d.GetId()
		}
	}
	results := make(chan error, 2)
	deleteDomain := func(id string) {
		_, deleteErr := f.orgs.DeleteOrgDomain(f.admin, connect.NewRequest(&orgv1.DeleteOrgDomainRequest{
			OrgId: f.orgID, DomainId: id,
		}))
		results <- deleteErr
	}

	paused, resume := f.sso.pauseNextDomainSync()
	go deleteDomain(first)
	<-paused
	go deleteDomain(second.GetId())
	finished := blockedOrDone(t, f.pool, results)
	close(resume)
	for len(finished) < 2 {
		finished = append(finished, <-results)
	}

	succeeded := 0
	for _, deleteErr := range finished {
		switch {
		case deleteErr == nil:
			succeeded++
		case codeOf(deleteErr) != connect.CodeFailedPrecondition:
			t.Fatalf("delete: %v", deleteErr)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d of two racing deletions succeeded, want exactly one", succeeded)
	}
	remaining, err := f.queries.ListVerifiedOrgDomainNames(t.Context(), uuid.MustParse(f.orgID))
	if err != nil || len(remaining) != 1 {
		t.Fatalf("verified domains left = %v (%v)", remaining, err)
	}
	f.providerDomains(t, remaining...)
}
