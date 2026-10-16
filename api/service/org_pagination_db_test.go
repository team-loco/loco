package service

import (
	"bytes"
	"slices"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

func TestOrgPaginationSurvivesDeletingTheCursorRow(t *testing.T) {
	f := newEventFixture(t)
	original, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
	if err != nil {
		t.Fatal(err)
	}
	server := NewOrgServer(f.pool, f.queries, f.machine)
	position := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	ids := make([]uuid.UUID, 3)
	for i := range ids {
		created, createErr := server.CreateOrg(f.owner, &orgv1.CreateOrgRequest{Name: new("page-" + strconv.Itoa(i))})
		if createErr != nil {
			t.Fatal(createErr)
		}
		ids[i] = uuid.MustParse(created.GetOrgId())
		if _, updateErr := f.pool.Exec(
			t.Context(),
			"UPDATE organizations SET created_at=$1 WHERE id=$2",
			position,
			ids[i],
		); updateErr != nil {
			t.Fatal(updateErr)
		}
	}
	unlisted, err := f.queries.CreateOrg(
		t.Context(),
		genDb.CreateOrgParams{Name: "unlisted", CreatedBy: original.CreatedBy},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, updateErr := f.pool.Exec(
		t.Context(),
		"UPDATE organizations SET created_at=$1 WHERE id=$2",
		position.Add(time.Minute),
		unlisted.ID,
	); updateErr != nil {
		t.Fatal(updateErr)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(b[:], a[:]) })
	ctx := scoped(t, f.queries, original.CreatedBy)
	page := func(token string) ([]*orgv1.Organization, string) {
		t.Helper()
		response, listErr := server.ListUserOrgs(ctx, &orgv1.ListUserOrgsRequest{
			UserId: original.CreatedBy.String(), PageSize: 1, PageToken: token,
		})
		if listErr != nil {
			t.Fatal(listErr)
		}
		return response.GetOrgs(), response.GetNextPageToken()
	}
	rows, token := page("")
	if len(rows) != 1 || rows[0].GetId() != ids[0].String() {
		t.Fatalf("first page=%v, want %s", rows, ids[0])
	}
	legacy, _ := page(encodeCursor(ids[0].String()))
	if len(legacy) != 1 || legacy[0].GetId() != ids[1].String() {
		t.Fatalf("legacy next page=%v, want %s", legacy, ids[1])
	}
	if _, deleteErr := server.DeleteOrg(ctx, &orgv1.DeleteOrgRequest{OrgId: ids[0].String()}); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	rows, token = page(token)
	if len(rows) != 1 || rows[0].GetId() != ids[1].String() {
		t.Fatalf("next page after deleting cursor=%v, want %s", rows, ids[1])
	}
	rows, _ = page(token)
	if len(rows) != 1 || rows[0].GetId() != ids[2].String() {
		t.Fatalf("third page=%v, want %s", rows, ids[2])
	}
}

func TestLegacyOrgCursorRejectsMissingAnchor(t *testing.T) {
	f := newEventFixture(t)
	original, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
	if err != nil {
		t.Fatal(err)
	}
	server := NewOrgServer(f.pool, f.queries, f.machine)
	_, err = server.ListUserOrgs(f.owner, &orgv1.ListUserOrgsRequest{
		UserId: original.CreatedBy.String(), PageSize: 1, PageToken: encodeCursor(uuid.NewString()),
	})
	wantCode(t, err, connect.CodeInvalidArgument)
}
