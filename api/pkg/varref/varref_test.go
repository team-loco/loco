package varref_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/team-loco/loco/api/pkg/varref"
)

const (
	stripeRef   = "${{ secrets.STRIPE_KEY }}"
	databaseURL = "DATABASE_URL"
	self        = "SELF"
)

func environment() varref.Environment {
	return varref.Environment{
		Secrets: map[string]bool{"DB_PASSWORD": true, "STRIPE_KEY": true},
		Resources: map[string]varref.Target{
			"api": {Port: 8080, PublicHost: "api.example.com", Env: map[string]string{
				"LOG_LEVEL": "info",
				"STRIPE":    stripeRef,
				"AUTH_URL":  "${{ auth.PRIVATE_URL }}/v1",
				databaseURL: "postgres://app:${{ secrets.DB_PASSWORD }}@${{ postgres.PRIVATE_HOST }}:5432/app",
			}},
			"auth":     {Port: 9000},
			"postgres": {},
		},
	}
}

func TestResolveSplitsPlainAndSecretVariables(t *testing.T) {
	got, err := environment().Resolve("web", varref.Target{Port: 3000}, map[string]string{
		"API_URL":   "${{ api.PRIVATE_URL }}",
		"PUBLIC":    "${{api.PUBLIC_URL}}",
		"AUTH":      "${{ api.AUTH_URL }}",
		self:        "${{ web.PORT }}",
		"KEY":       stripeRef,
		databaseURL: "${{ api.DATABASE_URL }}",
		"PLAIN":     "x",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	wantPlain := map[string]string{
		"API_URL": "http://api:8080",
		"PUBLIC":  "https://api.example.com",
		"AUTH":    "http://auth:9000/v1",
		self:      "3000",
		"PLAIN":   "x",
	}
	if !maps.Equal(got.Plain, wantPlain) {
		t.Errorf("Plain = %v, want %v", got.Plain, wantPlain)
	}
	wantSecret := map[string]string{
		"KEY":       stripeRef,
		databaseURL: "postgres://app:${{ secrets.DB_PASSWORD }}@postgres:5432/app",
	}
	if !maps.Equal(got.Secret, wantSecret) {
		t.Errorf("Secret = %v, want %v", got.Secret, wantSecret)
	}
	if want := []string{"DB_PASSWORD", "STRIPE_KEY"}; !slices.Equal(got.SecretNames(), want) {
		t.Errorf("SecretNames() = %v, want %v", got.SecretNames(), want)
	}
	if want := []string{"api"}; !slices.Equal(got.Resources, want) {
		t.Errorf("Resources = %v, want %v", got.Resources, want)
	}
}

func TestResolveRejectsBadReferences(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  error
	}{
		{"missing secret", "${{ secrets.NOPE }}", varref.ErrUnknownSecret},
		{"missing resource", "${{ cart.PORT }}", varref.ErrUnknownResource},
		{"missing variable", "${{ api.NOPE }}", varref.ErrUnknownOutput},
		{"worker has no port", "${{ postgres.PORT }}", varref.ErrUnknownOutput},
		{"malformed", "${{ api }}", varref.ErrMalformed},
		{"lowercase source required", "${{ API.PORT }}", varref.ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := environment().Resolve("web", varref.Target{}, map[string]string{"X": tt.value})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, tt.want)
			}
			variables := varref.VariableErrors(err)
			if len(variables) != 1 || variables[0].Name != "X" {
				t.Errorf("VariableErrors() = %v, want one for X", variables)
			}
		})
	}
}

func TestResolveRejectsCycles(t *testing.T) {
	env := environment()
	env.Resources["api"] = varref.Target{Env: map[string]string{"A": "${{ web.B }}"}}
	_, err := env.Resolve("web", varref.Target{}, map[string]string{
		"B":  "${{ api.A }}",
		self: "${{ web.SELF }}",
		"OK": "fine",
	})
	if !errors.Is(err, varref.ErrCycle) {
		t.Fatalf("Resolve() error = %v, want %v", err, varref.ErrCycle)
	}
	variables := varref.VariableErrors(err)
	names := make([]string, 0, len(variables))
	for _, variable := range variables {
		names = append(names, variable.Name)
	}
	if want := []string{"B", self}; !slices.Equal(names, want) {
		t.Errorf("failing variables = %v, want %v", names, want)
	}
}

func TestRenderReplacesSecretReferences(t *testing.T) {
	values := map[string]string{"A": "1", "B": "2"}
	got, err := varref.Render("x=${{ secrets.A }};y=${{secrets.B}}", func(ref varref.Ref) (string, error) {
		return values[ref.Key], nil
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "x=1;y=2" {
		t.Errorf("Render() = %q, want %q", got, "x=1;y=2")
	}
}
