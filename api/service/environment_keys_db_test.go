package service

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

const testEnvironmentName = "Staging EU"

func createKeyedEnvironment(
	t *testing.T, f *secretFixture, workspaceID, name string, slug *string,
) (genDb.Environment, error) {
	t.Helper()
	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	created, err := server.CreateEnvironment(f.owner, &environmentv1.CreateEnvironmentRequest{
		WorkspaceId: workspaceID, Name: name, Slug: slug,
	})
	if err != nil {
		return genDb.Environment{}, err
	}
	return f.queries.GetEnvironmentByID(t.Context(), uuid.MustParse(created.GetEnvironmentId()))
}

func TestCreateEnvironmentStoresAFreeTextNameAndASlug(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	workspaceID := f.workspace(t, "keys").String()
	for _, tc := range []struct {
		name, wantName, wantSlug string
		slug                     *string
	}{
		{name: " " + testEnvironmentName + " ", wantName: testEnvironmentName, wantSlug: "staging-eu"},
		{name: "QA 🚀", slug: new("qa"), wantName: "QA 🚀", wantSlug: "qa"},
	} {
		env, err := createKeyedEnvironment(t, f, workspaceID, tc.name, tc.slug)
		if err != nil {
			t.Fatalf("%q: %v", tc.name, err)
		}
		if env.Name != tc.wantName || env.Slug != tc.wantSlug {
			t.Fatalf("%q: name, slug = %q, %q, want %q, %q", tc.name, env.Name, env.Slug, tc.wantName, tc.wantSlug)
		}
	}
	if _, err := f.pool.Exec(
		t.Context(), `UPDATE environments SET slug = 'Staging EU' WHERE workspace_id = $1`, workspaceID,
	); err == nil {
		t.Fatal("the database stored an environment slug that is not a DNS label")
	}
}

func TestCreateEnvironmentRejectsInvalidKeys(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	workspaceID := f.workspace(t, "invalid").String()
	for _, name := range []string{"", "   ", strings.Repeat("a", 64)} {
		_, err := createKeyedEnvironment(t, f, workspaceID, name, new("valid"))
		wantMessage(t, err, connect.CodeInvalidArgument, errInvalidEnvironmentName.Error())
	}
	for _, slug := range []string{
		"", "Staging EU", "staging_eu", "-staging", "staging-", strings.Repeat("a", 64),
		testLocalhost, testReservedServiceName,
	} {
		_, err := createKeyedEnvironment(t, f, workspaceID, "Valid", new(slug))
		wantMessage(t, err, connect.CodeInvalidArgument, errInvalidEnvironmentSlug.Error())
	}
	_, err := createKeyedEnvironment(t, f, workspaceID, "!!!", nil)
	wantMessage(t, err, connect.CodeInvalidArgument, errInvalidEnvironmentSlug.Error())
}

func TestEnvironmentNamesAndSlugsAreUniquePerWorkspace(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	workspaceID := f.workspace(t, "unique").String()
	staging, err := createKeyedEnvironment(t, f, workspaceID, "Staging", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = createKeyedEnvironment(t, f, workspaceID, "STAGING", new("other"))
	wantMessage(t, err, connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique.Error()+": STAGING")
	_, err = createKeyedEnvironment(t, f, workspaceID, "Other", new("staging"))
	wantMessage(t, err, connect.CodeAlreadyExists, ErrEnvironmentSlugNotUnique.Error()+": staging")

	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	update := func(name string) error {
		_, updateErr := server.UpdateEnvironment(f.owner, &environmentv1.UpdateEnvironmentRequest{
			EnvironmentId: staging.ID.String(), Name: new(name),
		})
		return updateErr
	}
	err = update(defaultEnvironmentName)
	wantMessage(t, err, connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique.Error()+": "+defaultEnvironmentName)
	err = update(" ")
	wantMessage(t, err, connect.CodeInvalidArgument, errInvalidEnvironmentName.Error())
	if err = update(" " + testEnvironmentName + " "); err != nil {
		t.Fatal(err)
	}
	renamed, err := f.queries.GetEnvironmentByID(t.Context(), staging.ID)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != testEnvironmentName || renamed.Slug != "staging" {
		t.Fatalf("name, slug = %q, %q after a rename", renamed.Name, renamed.Slug)
	}
}
