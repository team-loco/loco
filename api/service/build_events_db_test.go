package service

import (
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

const maxBuildsListed = 10

type buildEventRow struct {
	eventType   string
	subjectID   uuid.UUID
	workspaceID *uuid.UUID
	orgID       *uuid.UUID
	resourceID  *uuid.UUID
	data        map[string]any
}

func (f *buildFixture) buildEvents(t *testing.T) []buildEventRow {
	t.Helper()
	rows, err := f.pool.Query(
		t.Context(),
		`SELECT type, subject_id, workspace_id, org_id, resource_id, data FROM events WHERE subject_type = $1 ORDER BY seq`,
		events.SubjectBuild,
	)
	if err != nil {
		t.Fatalf("list build events: %v", err)
	}
	defer rows.Close()
	var out []buildEventRow
	for rows.Next() {
		var row buildEventRow
		if scanErr := rows.Scan(
			&row.eventType,
			&row.subjectID,
			&row.workspaceID,
			&row.orgID,
			&row.resourceID,
			&row.data,
		); scanErr != nil {
			t.Fatalf("scan build event: %v", scanErr)
		}
		out = append(out, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read build events: %v", rowsErr)
	}
	return out
}

func (f *buildFixture) wantBuildOutcome(t *testing.T, id, kind, key, value string) {
	t.Helper()
	var outcomes []buildEventRow
	for _, row := range f.buildEvents(t) {
		if row.subjectID.String() == id &&
			(row.eventType == events.BuildSucceeded || row.eventType == events.BuildFailed) {
			outcomes = append(outcomes, row)
		}
	}
	if len(outcomes) != 1 || outcomes[0].eventType != kind {
		t.Fatalf("build %s outcomes = %v, want one %s event", id, outcomes, kind)
	}
	row := outcomes[0]
	resourceID := f.row(t, id).ResourceID
	if row.resourceID == nil || *row.resourceID != resourceID ||
		row.workspaceID == nil || *row.workspaceID != f.workspaceID(t) ||
		row.orgID == nil || row.orgID.String() != f.organizationID(t) {
		t.Fatalf("build %s outcome scope = %+v", id, row)
	}
	want := map[string]any{events.FieldBuildID: id, events.FieldResourceID: resourceID.String(), key: value}
	if !maps.Equal(row.data, want) {
		t.Fatalf("build %s outcome data = %v, want %v", id, row.data, want)
	}
}

func TestBuildMutationsRecordEvents(t *testing.T) {
	f := newBuildFixture(t)
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}

	created := f.create(t, 100)
	buildID := created.GetBuildId()
	f.uploadFor(t, buildID, 100)
	if _, startErr := f.start(buildID); startErr != nil {
		t.Fatalf("start build: %v", startErr)
	}
	if _, cancelErr := f.cancel(buildID); cancelErr != nil {
		t.Fatalf("cancel build: %v", cancelErr)
	}

	got := f.buildEvents(t)
	types := make([]string, len(got))
	for i, row := range got {
		types[i] = row.eventType
		if row.resourceID == nil || *row.resourceID != resource.ID {
			t.Fatalf("%s resource = %v, want %s", row.eventType, row.resourceID, resource.ID)
		}
		if row.subjectID.String() != buildID {
			t.Fatalf("%s subject = %s, want build %s", row.eventType, row.subjectID, buildID)
		}
		if row.workspaceID == nil || *row.workspaceID != resource.WorkspaceID || row.orgID == nil {
			t.Fatalf("%s scope = workspace %v org %v, want workspace %s and its org",
				row.eventType, row.workspaceID, row.orgID, resource.WorkspaceID)
		}
	}
	want := []string{events.BuildCreated, events.BuildStarted, events.BuildCanceled}
	if !slices.Equal(types, want) {
		t.Fatalf("build events = %v, want %v", types, want)
	}
}

func TestBuildMutationsFailWithoutTheirEvents(t *testing.T) {
	f := newBuildFixture(t)
	created := f.create(t, 100)
	buildID := created.GetBuildId()
	f.uploadFor(t, buildID, 100)
	if _, err := f.pool.Exec(t.Context(), failEventInsertsSQL); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}

	resourceID := f.resourceID.String()
	createReq := &buildv1.CreateBuildRequest{
		ResourceId:     resourceID,
		DockerfilePath: testDockerfile,
		SourceSize:     100,
	}
	if _, err := f.server.CreateBuild(f.ctx, createReq); err == nil {
		t.Fatal("create succeeded without its event")
	}
	builds, err := f.queries.ListBuildsForResource(t.Context(), genDb.ListBuildsForResourceParams{
		ResourceID: f.resourceID,
		Limit:      maxBuildsListed,
	})
	if err != nil || len(builds) != 1 {
		t.Fatalf("builds after failed create = %d (%v), want 1", len(builds), err)
	}

	if _, err := f.start(buildID); err == nil {
		t.Fatal("start succeeded without its event")
	}
	wantStatus(t, f.get(t, buildID), buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD)

	if _, err := f.cancel(buildID); err == nil {
		t.Fatal("cancel succeeded without its event")
	}
	wantStatus(t, f.get(t, buildID), buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD)
}
