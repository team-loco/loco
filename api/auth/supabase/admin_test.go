package supabase_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/supabase"
)

const (
	stateActive  = "active"
	stateExpired = "expired"
	stateBanned  = "banned"
	stateDeleted = "deleted"

	subjectConfirmed    = "confirmed"
	subjectSelfAsserted = "self-asserted"
	subjectChanged      = "changed"
	adminTokenEnv       = "SERVICE_KEY"
	fieldEmail          = "email"
	fieldEmailVerified  = "email_verified"
	fieldConfirmedAt    = "email_confirmed_at"
	fieldAppMetadata    = "app_metadata"
	fieldProviders      = "providers"
	bearerServiceKey    = "Bearer service-key"
	usersPath           = "/admin/users/"
	ssoProvidersPath    = "/admin/sso/providers/"
	ssoEmail            = "dana@example.test"

	subjectSSORegistered   = "sso-registered"
	subjectSSOUnregistered = "sso-unregistered"
	subjectSSONoDomains    = "sso-no-domains"
	subjectSSOMissing      = "sso-missing-provider"
	subjectSSOTwoProviders = "sso-two-providers"
	providerRegistered     = "0b7f3c1e-9a52-4d6b-8f0e-1c2d3e4f5a60"
	providerOtherDomain    = "6a1d2e3f-4b5c-4d7e-9f80-a1b2c3d4e5f6"
	providerNoDomains      = "c4e5f6a7-b8c9-4dae-8bf0-123456789abc"
	providerMissing        = "f0e1d2c3-b4a5-4968-8776-655443322110"
)

var factories = auth.AdminFactories{supabase.AdminType: supabase.NewAdmin}

func TestAdmin(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	users := map[string]map[string]any{
		stateActive:  {"id": stateActive},
		stateExpired: {"id": stateExpired, "banned_until": past},
		stateBanned:  {"id": stateBanned, "banned_until": future},
		stateDeleted: {"id": stateDeleted, "deleted_at": past},
	}
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer service-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/users/")
		if r.Method == http.MethodDelete {
			deleted = append(deleted, id)
			w.WriteHeader(http.StatusOK)
			return
		}
		user, ok := users[id]
		if !ok {
			http.Error(w, `{"msg":"User not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(user); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	admins, err := auth.NewAdmins(http.DefaultClient, []auth.IssuerConfig{{
		Issuer: srv.URL,
		Admin:  &auth.AdminConfig{Type: supabase.AdminType, URL: srv.URL, TokenEnv: "KEY", Token: "service-key"},
	}}, factories)
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	admin, ok := admins.For(srv.URL)
	if !ok {
		t.Fatal("no admin for issuer")
	}

	for subject, want := range map[string]auth.IdentityState{
		stateActive:  auth.IdentityActive,
		stateExpired: auth.IdentityActive,
		stateBanned:  auth.IdentityDisabled,
		stateDeleted: auth.IdentityMissing,
		"missing":    auth.IdentityMissing,
	} {
		got, stateErr := admin.State(t.Context(), subject)
		if stateErr != nil || got != want {
			t.Errorf("State(%s) = %v %v, want %v", subject, got, stateErr, want)
		}
	}
	if deleteErr := admin.Delete(
		t.Context(),
		stateActive,
	); deleteErr != nil || len(deleted) != 1 ||
		deleted[0] != stateActive {
		t.Fatalf("delete: %v %v", deleteErr, deleted)
	}

	bad, err := auth.NewAdmins(http.DefaultClient, []auth.IssuerConfig{{
		Issuer: srv.URL,
		Admin:  &auth.AdminConfig{Type: supabase.AdminType, URL: srv.URL, TokenEnv: "KEY", Token: "wrong"},
	}}, factories)
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	wrong, ok := bad.For(srv.URL)
	if !ok {
		t.Fatal("no admin")
	}
	if _, err := wrong.State(t.Context(), stateActive); err == nil {
		t.Fatal("unauthorized lookup reported no error")
	}
}

func TestAdminEmailVerified(t *testing.T) {
	confirmed := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	users := map[string]map[string]any{
		subjectConfirmed: {"id": subjectConfirmed, fieldEmail: "dana@example.test", fieldConfirmedAt: confirmed},
		subjectSelfAsserted: {
			"id":            subjectSelfAsserted,
			fieldEmail:      "dana@example.test",
			"user_metadata": map[string]any{fieldEmailVerified: true},
		},
		subjectChanged: {"id": subjectChanged, fieldEmail: "new@example.test", fieldConfirmedAt: confirmed},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != bearerServiceKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, ok := users[strings.TrimPrefix(r.URL.Path, usersPath)]
		if !ok {
			http.Error(w, `{"msg":"User not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(user); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	verifier := newEmailVerifier(t, srv.URL)
	for subject, want := range map[string]bool{
		subjectConfirmed:    true,
		subjectSelfAsserted: false,
		subjectChanged:      false,
		"missing":           false,
	} {
		got, verifyErr := verifier.EmailVerified(t.Context(), subject, "Dana@Example.test")
		if verifyErr != nil || got != want {
			t.Errorf("EmailVerified(%s) = %v %v, want %v", subject, got, verifyErr, want)
		}
	}
}

func newEmailVerifier(t *testing.T, url string) auth.EmailVerifier {
	t.Helper()
	issuers := []auth.IssuerConfig{{
		Issuer:            url,
		EmailVerification: auth.EmailVerificationAdmin,
		Admin: &auth.AdminConfig{
			Type: supabase.AdminType, URL: url, TokenEnv: adminTokenEnv, Token: "service-key",
		},
	}}
	admins, err := auth.NewAdmins(http.DefaultClient, issuers, factories)
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	verifiers, err := auth.NewEmailVerifiers(issuers, admins)
	if err != nil {
		t.Fatalf("email verifiers: %v", err)
	}
	verifier, ok := verifiers[url]
	if !ok {
		t.Fatal("no email verifier for issuer")
	}
	return verifier
}

func ssoUser(subject string, providerIDs ...string) map[string]any {
	providers := make([]string, 0, len(providerIDs))
	for _, id := range providerIDs {
		providers = append(providers, "sso:"+id)
	}
	return map[string]any{
		"id":             subject,
		fieldEmail:       ssoEmail,
		fieldConfirmedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		fieldAppMetadata: map[string]any{"provider": providers[0], fieldProviders: providers},
	}
}

func ssoProvider(id string, domains ...string) map[string]any {
	registered := make([]map[string]string, 0, len(domains))
	for _, domain := range domains {
		registered = append(registered, map[string]string{"domain": domain})
	}
	return map[string]any{"id": id, "domains": registered}
}

func TestAdminEmailVerifiedSSODomains(t *testing.T) {
	users := map[string]map[string]any{
		subjectSSORegistered:   ssoUser(subjectSSORegistered, providerRegistered),
		subjectSSOUnregistered: ssoUser(subjectSSOUnregistered, providerOtherDomain),
		subjectSSONoDomains:    ssoUser(subjectSSONoDomains, providerNoDomains),
		subjectSSOMissing:      ssoUser(subjectSSOMissing, providerMissing),
		subjectSSOTwoProviders: ssoUser(subjectSSOTwoProviders, providerRegistered, providerOtherDomain),
	}
	providers := map[string]map[string]any{
		providerRegistered:  ssoProvider(providerRegistered, "example.test"),
		providerOtherDomain: ssoProvider(providerOtherDomain, "other.test"),
		providerNoDomains:   ssoProvider(providerNoDomains),
	}
	var providerRequests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != bearerServiceKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		var found bool
		if id, isProvider := strings.CutPrefix(r.URL.Path, ssoProvidersPath); isProvider {
			providerRequests = append(providerRequests, r.URL.Path)
			body, found = providers[id]
		} else {
			body, found = users[strings.TrimPrefix(r.URL.Path, usersPath)]
		}
		if !found {
			http.Error(w, `{"msg":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	verifier := newEmailVerifier(t, srv.URL)

	got, err := verifier.EmailVerified(t.Context(), subjectSSORegistered, ssoEmail)
	if err != nil || !got {
		t.Fatalf("registered domain = %v %v, want true", got, err)
	}
	if want := ssoProvidersPath + providerRegistered; len(providerRequests) != 1 || providerRequests[0] != want {
		t.Fatalf("provider requests = %v, want [%s]", providerRequests, want)
	}

	for _, subject := range []string{
		subjectSSOUnregistered,
		subjectSSONoDomains,
		subjectSSOMissing,
		subjectSSOTwoProviders,
	} {
		denied, verifyErr := verifier.EmailVerified(t.Context(), subject, ssoEmail)
		if verifyErr != nil || denied {
			t.Errorf("EmailVerified(%s) = %v %v, want false", subject, denied, verifyErr)
		}
	}

	upper, err := verifier.EmailVerified(t.Context(), subjectSSORegistered, "dana@EXAMPLE.TEST")
	if err != nil || !upper {
		t.Errorf("uppercase domain = %v %v, want true", upper, err)
	}
}
