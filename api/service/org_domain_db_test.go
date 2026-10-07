package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

const corpDomain = "corp.test"

type domainFixture struct {
	pool     *pgxpool.Pool
	queries  *genDb.Queries
	machine  *tvm.VendingMachine
	orgs     *OrgServer
	resolver *auth.Resolver
	txt      map[string][]string
	owner    context.Context
	orgID    string
}

func newDomainFixture(t *testing.T) *domainFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &domainFixture{
		pool:     pool,
		queries:  queries,
		machine:  machine,
		orgs:     NewOrgServer(pool, queries, machine),
		resolver: auth.NewResolver(pool, policy),
		txt:      map[string][]string{},
	}
	f.orgs.lookupTXT = func(_ context.Context, name string) ([]string, error) {
		records, ok := f.txt[name]
		if !ok {
			return nil, errors.New("no such host")
		}
		return records, nil
	}
	owner := f.signUp(t, "owner", "owner@corp.test", true)
	ownerCtx := scoped(t, machine, owner)
	created, err := f.orgs.CreateOrg(ownerCtx, connect.NewRequest(&orgv1.CreateOrgRequest{Name: new("corp")}))
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	f.orgID = created.Msg.GetOrgId()
	f.owner = scoped(t, machine, owner)
	return f
}

func (f *domainFixture) signUp(t *testing.T, sub, email string, verified bool) uuid.UUID {
	t.Helper()
	user, err := f.resolver.Resolve(t.Context(), auth.Identity{
		Issuer: testIssuer, Subject: sub, Email: email, EmailVerified: verified,
	})
	if err != nil {
		t.Fatalf("sign up %s: %v", email, err)
	}
	return user.ID
}

func (f *domainFixture) orgScopes(t *testing.T, userID uuid.UUID) []genDb.Scope {
	t.Helper()
	scopes, err := f.machine.UserScopes(t.Context(), userID)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	var out []genDb.Scope
	for _, s := range scopes {
		if s.EntityType == genDb.EntityTypeOrganization && s.EntityID.String() == f.orgID {
			out = append(out, s.Scope)
		}
	}
	return out
}

func (f *domainFixture) addVerified(t *testing.T, domain string) *orgv1.OrgDomain {
	t.Helper()
	added, err := f.orgs.AddOrgDomain(
		f.owner,
		connect.NewRequest(&orgv1.AddOrgDomainRequest{OrgId: f.orgID, Domain: domain}),
	)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	d := added.Msg.GetDomain()
	f.txt[d.GetVerificationRecordName()] = []string{"unrelated", d.GetVerificationRecordValue()}
	verified, err := f.orgs.VerifyOrgDomain(f.owner, connect.NewRequest(&orgv1.VerifyOrgDomainRequest{
		OrgId: f.orgID, DomainId: d.GetId(),
	}))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return verified.Msg.GetDomain()
}

func TestOrgDomainVerification(t *testing.T) {
	f := newDomainFixture(t)

	added, err := f.orgs.AddOrgDomain(f.owner, connect.NewRequest(&orgv1.AddOrgDomainRequest{
		OrgId: f.orgID, Domain: "Corp.Test.",
	}))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	d := added.Msg.GetDomain()
	if d.GetDomain() != corpDomain || d.GetVerified() ||
		d.GetVerificationRecordName() != "_loco-verification.corp.test" {
		t.Fatalf("domain = %+v", d)
	}
	if _, callErr := f.orgs.AddOrgDomain(f.owner, connect.NewRequest(&orgv1.AddOrgDomainRequest{
		OrgId: f.orgID, Domain: corpDomain,
	})); codeOf(callErr) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate add: %v", callErr)
	}

	verify := connect.NewRequest(&orgv1.VerifyOrgDomainRequest{OrgId: f.orgID, DomainId: d.GetId()})
	if _, verifyErr := f.orgs.VerifyOrgDomain(f.owner, verify); codeOf(verifyErr) != connect.CodeFailedPrecondition {
		t.Fatalf("verify without record: %v", verifyErr)
	}
	f.txt[d.GetVerificationRecordName()] = []string{"loco-verification=wrong"}
	if _, verifyErr := f.orgs.VerifyOrgDomain(f.owner, verify); codeOf(verifyErr) != connect.CodeFailedPrecondition {
		t.Fatalf("verify with wrong record: %v", verifyErr)
	}
	f.txt[d.GetVerificationRecordName()] = []string{d.GetVerificationRecordValue()}
	verified, err := f.orgs.VerifyOrgDomain(f.owner, verify)
	if err != nil || !verified.Msg.GetDomain().GetVerified() {
		t.Fatalf("verify: %+v %v", verified, err)
	}

	rival := f.signUp(t, "rival", "rival@rival.test", true)
	rivalCtx := scoped(t, f.machine, rival)
	rivalOrg, err := f.orgs.CreateOrg(rivalCtx, connect.NewRequest(&orgv1.CreateOrgRequest{Name: new("rival")}))
	if err != nil {
		t.Fatalf("rival org: %v", err)
	}
	rivalCtx = scoped(t, f.machine, rival)
	claim, err := f.orgs.AddOrgDomain(rivalCtx, connect.NewRequest(&orgv1.AddOrgDomainRequest{
		OrgId: rivalOrg.Msg.GetOrgId(), Domain: corpDomain,
	}))
	if err != nil {
		t.Fatalf("rival add: %v", err)
	}
	f.txt[claim.Msg.GetDomain().GetVerificationRecordName()] = []string{
		claim.Msg.GetDomain().GetVerificationRecordValue(),
	}
	_, err = f.orgs.VerifyOrgDomain(rivalCtx, connect.NewRequest(&orgv1.VerifyOrgDomainRequest{
		OrgId: rivalOrg.Msg.GetOrgId(), DomainId: claim.Msg.GetDomain().GetId(),
	}))
	if codeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("second org verified a claimed domain: %v", err)
	}

	if _, callErr := f.orgs.ListOrgDomains(rivalCtx, connect.NewRequest(&orgv1.ListOrgDomainsRequest{
		OrgId: f.orgID,
	})); codeOf(callErr) != connect.CodePermissionDenied {
		t.Fatalf("outsider listed domains: %v", callErr)
	}
}

func TestOrgDomainAutoJoin(t *testing.T) {
	f := newDomainFixture(t)

	existing := f.signUp(t, "existing", "existing@corp.test", true)
	unverified := f.signUp(t, "unverified", "unverified@corp.test", false)
	outsider := f.signUp(t, "outsider", "outsider@elsewhere.test", true)

	added, err := f.orgs.AddOrgDomain(f.owner, connect.NewRequest(&orgv1.AddOrgDomainRequest{
		OrgId: f.orgID, Domain: corpDomain,
	}))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, callErr := f.orgs.SetOrgDomainAutoJoin(f.owner, connect.NewRequest(&orgv1.SetOrgDomainAutoJoinRequest{
		OrgId: f.orgID, DomainId: added.Msg.GetDomain().GetId(), Scope: tokenv1.Scope_SCOPE_READ,
	})); codeOf(callErr) != connect.CodeFailedPrecondition {
		t.Fatalf("auto-join on an unverified domain: %v", callErr)
	}
	if _, callErr := f.orgs.DeleteOrgDomain(f.owner, connect.NewRequest(&orgv1.DeleteOrgDomainRequest{
		OrgId: f.orgID, DomainId: added.Msg.GetDomain().GetId(),
	})); callErr != nil {
		t.Fatalf("delete: %v", callErr)
	}

	d := f.addVerified(t, corpDomain)
	set, err := f.orgs.SetOrgDomainAutoJoin(f.owner, connect.NewRequest(&orgv1.SetOrgDomainAutoJoinRequest{
		OrgId: f.orgID, DomainId: d.GetId(), Scope: tokenv1.Scope_SCOPE_WRITE,
	}))
	if err != nil {
		t.Fatalf("enable auto-join: %v", err)
	}
	if set.Msg.GetUsersAdded() != 1 || set.Msg.GetDomain().GetAutoJoinScope() != tokenv1.Scope_SCOPE_WRITE {
		t.Fatalf("auto-join = %+v", set.Msg)
	}
	if got := f.orgScopes(t, existing); len(got) != 2 {
		t.Fatalf("existing verified user scopes = %v", got)
	}
	if got := f.orgScopes(t, unverified); len(got) != 0 {
		t.Fatalf("unverified user joined: %v", got)
	}
	if got := f.orgScopes(t, outsider); len(got) != 0 {
		t.Fatalf("outsider joined: %v", got)
	}

	newcomer := f.signUp(t, "newcomer", "newcomer@corp.test", true)
	if got := f.orgScopes(t, newcomer); len(got) != 2 {
		t.Fatalf("new sign-up scopes = %v", got)
	}

	if _, callErr := f.orgs.SetOrgDomainAutoJoin(f.owner, connect.NewRequest(&orgv1.SetOrgDomainAutoJoinRequest{
		OrgId: f.orgID, DomainId: d.GetId(), Scope: tokenv1.Scope_SCOPE_UNSPECIFIED,
	})); callErr != nil {
		t.Fatalf("disable auto-join: %v", callErr)
	}
	late := f.signUp(t, "late", "late@corp.test", true)
	if got := f.orgScopes(t, late); len(got) != 0 {
		t.Fatalf("joined after auto-join was turned off: %v", got)
	}

	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{Seq: 0, Limit: 100})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	joins := 0
	for _, e := range all {
		if e.Type == "member.added" && e.ActorType == "system" {
			joins++
		}
	}
	if joins != 2 {
		t.Fatalf("auto-join events = %d", joins)
	}
}
