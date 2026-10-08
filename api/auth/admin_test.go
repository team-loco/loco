package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	serviceKeyEnv = "KEY"
	serviceKey    = "service-key"
	ssoDomain     = "corp.test"
	stateActive   = "active"
	stateExpired  = "expired"
	stateBanned   = "banned"
	stateDeleted  = "deleted"

	subjectConfirmed    = "confirmed"
	subjectSelfAsserted = "self-asserted"
	subjectChanged      = "changed"
	emailVerifierIssuer = "https://verifier.test"
)

func TestSupabaseAdmin(t *testing.T) {
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
		if r.Header.Get("Authorization") != "Bearer "+serviceKey {
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

	admins, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: srv.URL,
		Admin:  &AdminConfig{Type: presetSupabase, URL: srv.URL, TokenEnv: serviceKeyEnv},
	}}, func(string) string { return serviceKey })
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	admin, ok := admins.For(srv.URL)
	if !ok {
		t.Fatal("no admin for issuer")
	}

	for subject, want := range map[string]IdentityState{
		stateActive:  IdentityActive,
		stateExpired: IdentityActive,
		stateBanned:  IdentityDisabled,
		stateDeleted: IdentityMissing,
		"missing":    IdentityMissing,
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

	bad, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: srv.URL,
		Admin:  &AdminConfig{Type: presetSupabase, URL: srv.URL, TokenEnv: serviceKeyEnv},
	}}, func(string) string { return "wrong" })
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

func TestNewAdminsValidation(t *testing.T) {
	if _, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: "https://a.test", Admin: &AdminConfig{Type: presetSupabase, TokenEnv: "MISSING"},
	}}, func(string) string { return "" }); err == nil {
		t.Fatal("empty token accepted")
	}
	if _, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: "https://a.test", Admin: &AdminConfig{Type: "okta", TokenEnv: "X"},
	}}, func(string) string { return "t" }); err == nil {
		t.Fatal("unknown admin type accepted")
	}
}

func TestSupabaseSSOAdmin(t *testing.T) {
	providers := map[string][]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+serviceKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/admin/sso/providers"), "/")
		var body struct {
			Type        string   `json:"type"`
			MetadataURL string   `json:"metadata_url"`
			MetadataXML string   `json:"metadata_xml"`
			Domains     []string `json:"domains"`
		}
		if r.Body != nil && r.Method != http.MethodDelete {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		switch r.Method {
		case http.MethodPost:
			if body.Type != "saml" || body.MetadataXML == "" {
				w.WriteHeader(http.StatusBadRequest)
				respond(t, w, `{"code":400,"error_code":"validation_failed","msg":"SAML Metadata XML is invalid"}`)
				return
			}
			providers["p1"] = body.Domains
			w.WriteHeader(http.StatusCreated)
			respond(t, w, `{"id":"p1"}`)
		case http.MethodPut:
			if _, ok := providers[id]; !ok {
				http.Error(w, `{"msg":"SSO Identity Provider not found"}`, http.StatusNotFound)
				return
			}
			providers[id] = body.Domains
			respond(t, w, "{}")
		case http.MethodDelete:
			if _, ok := providers[id]; !ok {
				http.Error(w, `{"msg":"SSO Identity Provider not found"}`, http.StatusNotFound)
				return
			}
			delete(providers, id)
			respond(t, w, "{}")
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	admins, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: srv.URL,
		Admin:  &AdminConfig{Type: presetSupabase, URL: srv.URL, TokenEnv: serviceKeyEnv},
	}}, func(string) string { return serviceKey })
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	sso, ok := admins.SSO(srv.URL)
	if !ok {
		t.Fatal("supabase admin does not manage SSO")
	}
	if _, other := admins.SSO("https://elsewhere.test"); other {
		t.Fatal("SSO admin found for an unknown issuer")
	}
	ctx := t.Context()

	_, err = sso.CreateSAMLConnection(ctx, "", "", []string{ssoDomain})
	if rejected, ok := errors.AsType[*ProviderError](err); !ok || rejected.Message != "SAML Metadata XML is invalid" {
		t.Fatalf("invalid metadata error = %v", err)
	}
	id, err := sso.CreateSAMLConnection(ctx, "", "<EntityDescriptor/>", []string{ssoDomain})
	if err != nil || id != "p1" {
		t.Fatalf("create = %q, %v", id, err)
	}
	if setErr := sso.SetSAMLDomains(ctx, id, []string{ssoDomain, "corp.example"}); setErr != nil {
		t.Fatalf("set domains: %v", setErr)
	}
	if got := providers[id]; len(got) != 2 {
		t.Fatalf("domains = %v", got)
	}
	if setErr := sso.SetSAMLDomains(ctx, "missing", nil); !isProviderError(setErr) {
		t.Fatalf("update of a missing provider = %v", setErr)
	}
	if deleteErr := sso.DeleteSAMLConnection(ctx, id); deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}
	if deleteErr := sso.DeleteSAMLConnection(ctx, id); deleteErr != nil {
		t.Fatalf("deleting a deleted provider: %v", deleteErr)
	}
	metadata, acs := sso.ServiceProvider()
	if metadata != srv.URL+"/sso/saml/metadata" || acs != srv.URL+"/sso/saml/acs" {
		t.Fatalf("service provider = %s %s", metadata, acs)
	}
}

func isProviderError(err error) bool {
	rejected, ok := errors.AsType[*ProviderError](err)
	return ok && rejected.Message != ""
}

func respond(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write: %v", err)
	}
}

func TestSupabaseAdminEmailVerified(t *testing.T) {
	confirmed := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	users := map[string]map[string]any{
		subjectConfirmed: {"id": subjectConfirmed, claimEmail: "dana@example.test", "email_confirmed_at": confirmed},
		subjectSelfAsserted: {
			"id":            subjectSelfAsserted,
			claimEmail:      "dana@example.test",
			"user_metadata": map[string]any{"email_verified": true},
		},
		subjectChanged: {"id": subjectChanged, claimEmail: "new@example.test", "email_confirmed_at": confirmed},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+serviceKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, ok := users[strings.TrimPrefix(r.URL.Path, "/admin/users/")]
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

	issuers := []IssuerConfig{{
		Issuer:            srv.URL,
		EmailVerification: EmailVerificationAdmin,
		Admin:             &AdminConfig{Type: presetSupabase, URL: srv.URL, TokenEnv: serviceKeyEnv},
	}}
	admins, err := NewAdmins(http.DefaultClient, issuers, func(string) string { return serviceKey })
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	verifiers, err := NewEmailVerifiers(issuers, admins)
	if err != nil {
		t.Fatalf("email verifiers: %v", err)
	}
	verifier, ok := verifiers[srv.URL]
	if !ok {
		t.Fatal("no email verifier for issuer")
	}
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

func TestNewEmailVerifiersNeedsAnAdmin(t *testing.T) {
	_, err := NewEmailVerifiers([]IssuerConfig{{
		Issuer: emailVerifierIssuer, EmailVerification: EmailVerificationAdmin,
	}}, Admins{})
	if !errors.Is(err, ErrNoEmailVerifier) {
		t.Fatalf("err = %v, want %v", err, ErrNoEmailVerifier)
	}
	verifiers, err := NewEmailVerifiers([]IssuerConfig{{Issuer: emailVerifierIssuer}}, Admins{})
	if err != nil || len(verifiers) != 0 {
		t.Fatalf("claim issuer: %v %v", verifiers, err)
	}
}
