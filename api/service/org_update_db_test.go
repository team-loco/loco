package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const organizationUpdatedName = "organization-renamed"

func TestOrganizationUpdateRejectsInvalidMasksAtomically(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		value *string
	}{
		{name: "immutable", paths: []string{"created_by"}, value: new(organizationUpdatedName)},
		{name: "unknown", paths: []string{"unsupported_org_field"}, value: new(organizationUpdatedName)},
		{name: "mixed", paths: []string{updateNameField, "created_by"}, value: new(organizationUpdatedName)},
		{name: "missing name", paths: []string{updateNameField}},
		{name: "wildcard missing name", paths: []string{"*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEventFixture(t)
			server := NewOrgServer(f.pool, f.queries, f.machine)
			_, err := server.UpdateOrg(f.owner, &orgv1.UpdateOrgRequest{
				OrgId: f.orgID, UpdateMask: &fieldmaskpb.FieldMask{Paths: tc.paths}, Name: tc.value,
			})
			wantCode(t, err, connect.CodeInvalidArgument)
			row, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
			if err != nil {
				t.Fatal(err)
			}
			if row.Name != membershipOrgName {
				t.Fatalf("rejected update renamed organization to %q", row.Name)
			}
			var recorded int
			const updateEvents = `SELECT count(*) FROM events
WHERE org_id=$1 AND type=$2`
			if eventErr := f.pool.QueryRow(t.Context(), updateEvents, f.orgID, events.OrgUpdated).
				Scan(&recorded); eventErr != nil {
				t.Fatal(eventErr)
			}
			if recorded != 0 {
				t.Fatalf("rejected update recorded %d events", recorded)
			}
		})
	}
}

func TestOrganizationUpdateAcceptsNameMasksAndUnmaskedNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		mask *fieldmaskpb.FieldMask
	}{
		{name: "no field mask"},
		{name: "empty field mask", mask: &fieldmaskpb.FieldMask{}},
		{name: "explicit name", mask: &fieldmaskpb.FieldMask{Paths: []string{updateNameField}}},
		{name: "all mutable fields", mask: &fieldmaskpb.FieldMask{Paths: []string{"*"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEventFixture(t)
			server := NewOrgServer(f.pool, f.queries, f.machine)
			_, err := server.UpdateOrg(f.owner, &orgv1.UpdateOrgRequest{
				OrgId: f.orgID, UpdateMask: tc.mask, Name: new(organizationUpdatedName),
			})
			if err != nil {
				t.Fatal(err)
			}
			row, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
			if err != nil {
				t.Fatal(err)
			}
			if row.Name != organizationUpdatedName {
				t.Fatalf("organization name = %q", row.Name)
			}
		})
	}
}
