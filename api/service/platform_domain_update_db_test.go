package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	platformUpdatedName  = "renamed.example.com"
	platformOriginalName = "example.com"
)

func platformOperator(t *testing.T, f *deployFixture) (*DomainServer, context.Context, uuid.UUID) {
	t.Helper()
	c := newDomainClient(t, f)
	scopes := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeAdmin}}
	return c.server, context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes), c.platformDomainID
}

func TestPlatformDomainUpdateSelectsFields(t *testing.T) {
	for _, tc := range []struct {
		name       string
		domain     *string
		active     *bool
		paths      []string
		wantDomain string
		wantActive bool
	}{
		{
			name:       "activate",
			active:     new(true),
			paths:      []string{platformDomainActiveField},
			wantDomain: platformOriginalName,
			wantActive: true,
		},
		{
			name:       "rename preserves activity",
			domain:     new(platformUpdatedName),
			paths:      []string{platformDomainNameField},
			wantDomain: platformUpdatedName,
			wantActive: true,
		},
		{
			name:       "unmasked explicit false",
			active:     new(false),
			wantDomain: platformOriginalName,
		},
		{
			name:       "unmasked absent preserves activity",
			wantDomain: platformOriginalName,
			wantActive: true,
		},
		{
			name:       "unmasked rename",
			domain:     new(platformUpdatedName),
			wantDomain: platformUpdatedName,
			wantActive: true,
		},
		{
			name:       "wildcard",
			domain:     new(platformUpdatedName),
			active:     new(true),
			paths:      []string{"*"},
			wantDomain: platformUpdatedName,
			wantActive: true,
		},
		{
			name:       "unselected domain ignored",
			domain:     new(platformUpdatedName),
			active:     new(false),
			paths:      []string{platformDomainActiveField},
			wantDomain: platformOriginalName,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeployFixture(t)
			server, ctx, id := platformOperator(t, f)
			if tc.name == "activate" {
				if _, err := f.queries.DeactivatePlatformDomain(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			_, err := server.UpdatePlatformDomain(
				ctx,
				&domainv1.UpdatePlatformDomainRequest{
					Id:         id.String(),
					Domain:     tc.domain,
					IsActive:   tc.active,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: tc.paths},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			row, err := f.queries.GetPlatformDomain(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if row.Domain != tc.wantDomain || row.IsActive != tc.wantActive {
				t.Fatalf("domain=%q active=%v, want %q %v", row.Domain, row.IsActive, tc.wantDomain, tc.wantActive)
			}
		})
	}
}

func TestPlatformDomainUpdateRejectsInvalidChangesAtomically(t *testing.T) {
	cases := [][]string{
		{"created_at"},
		{platformDomainNameField, "created_at"},
		{platformDomainNameField},
		{"*"},
	}
	for _, paths := range cases {
		t.Run("mask_"+paths[0], func(t *testing.T) {
			f := newDeployFixture(t)
			server, ctx, id := platformOperator(t, f)
			_, err := server.UpdatePlatformDomain(
				ctx,
				&domainv1.UpdatePlatformDomainRequest{
					Id:         id.String(),
					IsActive:   new(false),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
				},
			)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("update error=%v, want invalid_argument", err)
			}
			row, err := f.queries.GetPlatformDomain(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if row.Domain != platformOriginalName || !row.IsActive {
				t.Fatalf("rejected update changed row: %+v", row)
			}
			var count int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM events WHERE subject_id=$1", id).
				Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("rejected update recorded %d events", count)
			}
		})
	}
}

func TestPlatformDomainUpdateMissingAndDuplicate(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx, id := platformOperator(t, f)
	_, err := server.UpdatePlatformDomain(
		ctx,
		&domainv1.UpdatePlatformDomainRequest{Id: uuid.NewString(), IsActive: new(true)},
	)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing update=%v, want not_found", err)
	}
	if _, createErr := f.queries.CreatePlatformDomain(
		ctx,
		genDb.CreatePlatformDomainParams{Domain: platformUpdatedName, IsActive: true},
	); createErr != nil {
		t.Fatal(createErr)
	}
	_, err = server.UpdatePlatformDomain(
		ctx,
		&domainv1.UpdatePlatformDomainRequest{Id: id.String(), Domain: new(platformUpdatedName)},
	)
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate update=%v, want already_exists", err)
	}
	row, err := f.queries.GetPlatformDomain(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Domain != platformOriginalName || !row.IsActive {
		t.Fatalf("duplicate update changed row: %+v", row)
	}
}

func TestPlatformDomainReferencedNameCannotChange(t *testing.T) {
	f := newDeployFixture(t)
	server, ctx, id := platformOperator(t, f)
	if _, err := f.queries.CreateResourceDomain(
		ctx,
		genDb.CreateResourceDomainParams{
			ResourceID:       f.resourceID,
			EnvironmentID:    f.envID,
			Domain:           "svc.example.com",
			DomainSource:     genDb.DomainSourcePlatformProvided,
			PlatformDomainID: &id,
			SubdomainLabel:   new("svc"),
			IsPrimary:        true,
		},
	); err != nil {
		t.Fatal(err)
	}
	_, err := server.UpdatePlatformDomain(
		ctx,
		&domainv1.UpdatePlatformDomainRequest{Id: id.String(), Domain: new(platformUpdatedName), IsActive: new(false)},
	)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("referenced rename=%v, want failed_precondition", err)
	}
	row, err := f.queries.GetPlatformDomain(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Domain != platformOriginalName || !row.IsActive {
		t.Fatalf("refused rename changed row: %+v", row)
	}
	_, err = server.UpdatePlatformDomain(
		ctx,
		&domainv1.UpdatePlatformDomainRequest{Id: id.String(), Domain: new(platformOriginalName), IsActive: new(false)},
	)
	if err != nil {
		t.Fatalf("deactivate referenced root without rename: %v", err)
	}
}
