//go:build conformance

package service

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

const (
	conformanceSupabase = "http://localhost:59999"
	conformanceIDP      = "http://localhost:58081"
	conformanceSite     = "http://localhost:5173"
	idpDomain           = "example.com"
	idpUser             = "user1"
	idpPassword         = "user1pass"
	idpEmail            = "user1@example.com"
	idpAccountPassword  = "conformance-password"
	sessionTTL          = time.Hour
	deviceWait          = 10 * time.Second
)

var (
	errNoForm        = errors.New("page has no form")
	errNoAccessToken = errors.New("sso redirect carried no access token")
	inputPattern     = regexp.MustCompile(`<input[^>]*>`)
	attrPattern      = regexp.MustCompile(`(name|value)="([^"]*)"`)
	actionPattern    = regexp.MustCompile(`<form[^>]*action="([^"]*)"`)
)

type htmlForm struct {
	action string
	fields url.Values
}

func parseForm(page string, base *url.URL) (htmlForm, error) {
	match := actionPattern.FindStringSubmatch(page)
	if match == nil {
		return htmlForm{}, errNoForm
	}
	ref, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil {
		return htmlForm{}, err
	}
	fields := url.Values{}
	for _, input := range inputPattern.FindAllString(page, -1) {
		var name, value string
		for _, attr := range attrPattern.FindAllStringSubmatch(input, -1) {
			if attr[1] == "name" {
				name = html.UnescapeString(attr[2])
			} else {
				value = html.UnescapeString(attr[2])
			}
		}
		if name != "" {
			fields.Set(name, value)
		}
	}
	return htmlForm{action: base.ResolveReference(ref).String(), fields: fields}, nil
}

type page struct {
	status int
	url    *url.URL
	body   string
}

func send(t *testing.T, client *http.Client, method, endpoint, contentType, body string) page {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", endpoint, err)
	}
	return page{status: res.StatusCode, url: res.Request.URL, body: string(raw)}
}

func submit(t *testing.T, client *http.Client, form htmlForm) page {
	t.Helper()
	return send(t, client, http.MethodPost, form.action, "application/x-www-form-urlencoded", form.fields.Encode())
}

func samlLogin(t *testing.T) string {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("jar: %v", err)
	}
	var landed *url.URL
	browser := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if strings.HasPrefix(req.URL.String(), conformanceSite) {
				landed = req.URL
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	start := postJSONBody(t, conformanceSupabase+"/sso", map[string]any{
		"domain": idpDomain, "redirect_to": conformanceSite + "/", "skip_http_redirect": true,
	})
	idpURL, ok := start["url"].(string)
	if !ok {
		t.Fatalf("sso start = %v", start)
	}
	loginPage := send(t, browser, http.MethodGet, idpURL, "", "")
	login, err := parseForm(loginPage.body, loginPage.url)
	if err != nil {
		t.Fatalf("idp login form: %v\n%s", err, loginPage.body)
	}
	login.fields.Set("username", idpUser)
	login.fields.Set("password", idpPassword)
	assertionPage := submit(t, browser, login)
	assertion, err := parseForm(assertionPage.body, assertionPage.url)
	if err != nil || assertion.fields.Get("SAMLResponse") == "" {
		t.Fatalf("idp assertion form: %v\n%s", err, assertionPage.body)
	}
	acs := submit(t, browser, assertion)
	if landed == nil {
		t.Fatalf("sso did not redirect to the site: %d", acs.status)
	}
	fragment, err := url.ParseQuery(landed.Fragment)
	if err != nil || fragment.Get("access_token") == "" {
		t.Fatalf("%v: %s (%v)", errNoAccessToken, landed.Redacted(), err)
	}
	return fragment.Get("access_token")
}

func postJSON(t *testing.T, endpoint string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	res := send(t, http.DefaultClient, http.MethodPost, endpoint, "application/json", string(raw))
	var out map[string]any
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("decode %s: %v: %s", endpoint, err, res.body)
	}
	return res.status, out
}

func postJSONBody(t *testing.T, endpoint string, body any) map[string]any {
	t.Helper()
	status, out := postJSON(t, endpoint, body)
	if status != http.StatusOK {
		t.Fatalf("post %s: %d %v", endpoint, status, out)
	}
	return out
}

func passwordLogin(t *testing.T) string {
	t.Helper()
	credentials := map[string]any{"email": idpEmail, "password": idpAccountPassword}
	status, session := postJSON(t, conformanceSupabase+"/signup", credentials)
	if status != http.StatusOK {
		session = postJSONBody(t, conformanceSupabase+"/token?grant_type=password", credentials)
	}
	token, ok := session["access_token"].(string)
	if !ok {
		t.Fatalf("password session = %v", session)
	}
	return token
}

func bearer[T any](msg *T, token string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)
	return req
}

type ssoStack struct {
	orgs orgv1connect.OrgServiceClient
	cli  authv1connect.AuthServiceClient
	auth *AuthServer
}

func newSSOStack(t *testing.T) (*ssoStack, *OrgServer) {
	t.Helper()
	issuers, err := auth.ParseIssuers(`[{
		"issuer": "` + conformanceSupabase + `",
		"audience": "authenticated",
		"claims": {"emailVerified": "user_metadata.email_verified"},
		"admin": {"type": "supabase", "tokenEnv": "AUTH_SUPABASE_SERVICE_KEY"}
	}]`)
	if err != nil {
		t.Fatalf("issuers: %v", err)
	}
	admins, err := auth.NewAdmins(http.DefaultClient, issuers, os.Getenv)
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	sso, ok := admins.SSO(conformanceSupabase)
	if !ok {
		t.Fatal("no sso admin")
	}
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  sessionTTL,
		SessionRefreshTokenDuration: sessionTTL,
		LastUsedUpdateInterval:      time.Minute,
		MaxAPITokenDuration:         sessionTTL,
	})
	t.Cleanup(machine.Close)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	store, err := cache.NewMemory(sessionTTL)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Logf("close cache: %v", closeErr)
		}
	})

	orgs := NewOrgServer(pool, queries, machine)
	orgs.UseSSO(sso, conformanceSupabase)
	authServer := NewAuthServer(queries, machine, store, admins, conformanceSite)
	verifier := auth.NewVerifier(http.DefaultClient, issuers, admins)
	gate := interceptor.NewAuthInterceptor(machine, verifier, auth.NewResolver(pool, policy), auth.NewSSOGate(queries))

	mux := http.NewServeMux()
	mux.Handle(orgv1connect.NewOrgServiceHandler(orgs, connect.WithInterceptors(gate)))
	mux.Handle(authv1connect.NewAuthServiceHandler(authServer, connect.WithInterceptors(gate)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &ssoStack{
		orgs: orgv1connect.NewOrgServiceClient(srv.Client(), srv.URL),
		cli:  authv1connect.NewAuthServiceClient(srv.Client(), srv.URL),
		auth: authServer,
	}, orgs
}

func (s *ssoStack) cliSession(t *testing.T, webToken string) string {
	t.Helper()
	ctx := t.Context()
	started, err := s.cli.StartDeviceLogin(ctx, connect.NewRequest(&authv1.StartDeviceLoginRequest{}))
	if err != nil {
		t.Fatalf("start device login: %v", err)
	}
	if _, approveErr := s.cli.ApproveDeviceLogin(ctx, bearer(&authv1.ApproveDeviceLoginRequest{
		UserCode: started.Msg.GetUserCode(),
	}, webToken)); approveErr != nil {
		t.Fatalf("approve device login: %v", approveErr)
	}
	now := time.Now().Add(deviceWait)
	s.auth.now = func() time.Time { return now }
	done, err := s.cli.PollDeviceLogin(ctx, connect.NewRequest(&authv1.PollDeviceLoginRequest{
		DeviceCode: started.Msg.GetDeviceCode(),
	}))
	if err != nil {
		t.Fatalf("poll device login: %v", err)
	}
	return done.Msg.GetTokens().GetAccessToken()
}

func fetchIDPMetadata(t *testing.T) string {
	t.Helper()
	metadataURL := conformanceIDP + "/simplesaml/saml2/idp/metadata.php"
	return send(t, http.DefaultClient, http.MethodGet, metadataURL, "", "").body
}

func TestSAMLSSOConformance(t *testing.T) {
	stack, orgs := newSSOStack(t)
	ctx := context.Background()
	txt := map[string][]string{}
	orgs.lookupTXT = func(_ context.Context, name string) ([]string, error) {
		return txt[name], nil
	}

	passwordToken := passwordLogin(t)
	created, err := stack.orgs.CreateOrg(
		ctx,
		bearer(&orgv1.CreateOrgRequest{Name: new("sso-conformance")}, passwordToken),
	)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	orgID := created.Msg.GetOrgId()
	added, err := stack.orgs.AddOrgDomain(
		ctx,
		bearer(&orgv1.AddOrgDomainRequest{OrgId: orgID, Domain: idpDomain}, passwordToken),
	)
	if err != nil {
		t.Fatalf("add domain: %v", err)
	}
	record := added.Msg.GetDomain()
	txt[record.GetVerificationRecordName()] = []string{record.GetVerificationRecordValue()}
	if _, verifyErr := stack.orgs.VerifyOrgDomain(ctx, bearer(&orgv1.VerifyOrgDomainRequest{
		OrgId: orgID, DomainId: record.GetId(),
	}, passwordToken)); verifyErr != nil {
		t.Fatalf("verify domain: %v", verifyErr)
	}
	configured, err := stack.orgs.ConfigureOrgSSO(ctx, bearer(&orgv1.ConfigureOrgSSORequest{
		OrgId: orgID, Metadata: &orgv1.ConfigureOrgSSORequest_MetadataXml{MetadataXml: fetchIDPMetadata(t)},
	}, passwordToken))
	if err != nil {
		t.Fatalf("configure sso: %v", err)
	}
	t.Cleanup(func() {
		ssoToken := samlLogin(t)
		if _, deleteErr := stack.orgs.DeleteOrgSSO(context.Background(), bearer(&orgv1.DeleteOrgSSORequest{
			OrgId: orgID,
		}, ssoToken)); deleteErr != nil {
			t.Errorf("remove sso: %v", deleteErr)
		}
	})

	require := &orgv1.SetOrgRequireSSORequest{OrgId: orgID, RequireSso: true}
	if _, requireErr := stack.orgs.SetOrgRequireSSO(
		ctx,
		bearer(require, passwordToken),
	); connect.CodeOf(
		requireErr,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("require sso from a password session: %v", requireErr)
	}

	ssoToken := samlLogin(t)
	state, err := stack.orgs.GetOrgSSO(ctx, bearer(&orgv1.GetOrgSSORequest{OrgId: orgID}, ssoToken))
	if err != nil || !state.Msg.GetSignedInWithSso() ||
		state.Msg.GetSso().GetConnectionId() != configured.Msg.GetSso().GetConnectionId() {
		t.Fatalf("sso login did not reach the org through its connection: %+v (%v)", state, err)
	}
	if _, requireErr := stack.orgs.SetOrgRequireSSO(ctx, bearer(require, ssoToken)); requireErr != nil {
		t.Fatalf("require sso: %v", requireErr)
	}

	passwordCLI := stack.cliSession(t, passwordToken)
	ssoCLI := stack.cliSession(t, ssoToken)
	cases := []struct {
		name    string
		token   string
		allowed bool
	}{
		{"password session", passwordToken, false},
		{"cli session from a password login", passwordCLI, false},
		{"saml session", ssoToken, true},
		{"cli session from a saml login", ssoCLI, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, getErr := stack.orgs.GetOrgSSO(ctx, bearer(&orgv1.GetOrgSSORequest{OrgId: orgID}, tc.token))
			if (getErr == nil) != tc.allowed {
				t.Fatalf("org access = %v, want allowed=%v", getErr, tc.allowed)
			}
		})
	}
}
