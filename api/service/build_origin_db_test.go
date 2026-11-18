package service

import (
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

func TestBuildOriginSurvivesEnvironmentDeletion(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{events.BuildSucceeded, events.BuildFailed, events.BuildCanceled} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			f := newBuildFixture(t)
			origin := f.addStagingEnvironment(t)
			created, err := f.server.CreateBuild(f.ctx, &buildv1.CreateBuildRequest{
				ResourceId: f.resourceID.String(), DockerfilePath: testDockerfile, SourceSize: 100,
				OriginEnvironmentId: new(origin.String()),
			})
			if err != nil {
				t.Fatal(err)
			}
			id := created.GetBuildId()
			if stored := f.row(t, id); stored.OriginEnvironmentID == nil || *stored.OriginEnvironmentID != origin {
				t.Fatalf("stored origin = %v, want %s", stored.OriginEnvironmentID, origin)
			}
			if deleteErr := f.queries.DeleteEnvironment(t.Context(), origin); deleteErr != nil {
				t.Fatal(deleteErr)
			}
			f.uploadFor(t, id, 100)
			started, err := f.start(id)
			if err != nil || started.GetOriginEnvironmentId() != origin.String() {
				t.Fatalf("started origin = %s, error %v", started.GetOriginEnvironmentId(), err)
			}
			if outcome == events.BuildCanceled {
				if _, cancelErr := f.cancel(id); cancelErr != nil {
					t.Fatal(cancelErr)
				}
			} else {
				session, _ := f.dispatchSession()
				phase := agentv1.BuildPhase_BUILD_PHASE_FAILED
				if outcome == events.BuildSucceeded {
					phase = agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED
				}
				report := buildStatus(id, phase)
				report.ImageDigest = testImageDigest
				if reportErr := session.handleBuildStatus(f.ctx, report); reportErr != nil {
					t.Fatal(reportErr)
				}
			}
			if outcome == events.BuildSucceeded {
				// The image remains a resource artifact, even after its initiating environment is gone.
				deployBuild(t, f, id)
			}
			if got := f.get(t, id).GetOriginEnvironmentId(); got != origin.String() {
				t.Fatalf("finished origin = %s, want deleted environment %s", got, origin)
			}
			rows, err := f.pool.Query(t.Context(),
				`SELECT type, environment_id FROM events WHERE subject_id=$1 ORDER BY seq`, uuid.MustParse(id))
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var kinds []string
			for rows.Next() {
				var kind string
				var eventOrigin *uuid.UUID
				if err := rows.Scan(&kind, &eventOrigin); err != nil {
					t.Fatal(err)
				}
				if eventOrigin == nil || *eventOrigin != origin {
					t.Fatalf("%s event origin = %v, want %s", kind, eventOrigin, origin)
				}
				kinds = append(kinds, kind)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := []string{events.BuildCreated, events.BuildStarted, outcome}
			if len(kinds) != len(want) {
				t.Fatalf("events = %v, want %v", kinds, want)
			}
			for i := range want {
				if kinds[i] != want[i] {
					t.Fatalf("events = %v, want %v", kinds, want)
				}
			}
		})
	}
}

func TestCreateBuildValidatesItsOriginBeforeIssuingAnUpload(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"unknown UUID", "another workspace", "malformed UUID"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newBuildFixture(t)
			origin := uuid.NewString()
			expectedCode := connect.CodeNotFound
			switch kind {
			case "another workspace":
				var workspace uuid.UUID
				if err := f.pool.QueryRow(t.Context(),
					`INSERT INTO workspaces(org_id,name,created_by)
SELECT org_id,'other-origin',$2 FROM workspaces WHERE id=$1 RETURNING id`,
					f.workspaceID(t), f.userID).Scan(&workspace); err != nil {
					t.Fatal(err)
				}
				env, err := f.queries.CreateEnvironment(t.Context(), genDb.CreateEnvironmentParams{
					WorkspaceID: workspace, Name: "origin", CreatedBy: f.userID,
				})
				if err != nil {
					t.Fatal(err)
				}
				origin = env.ID.String()
			case "malformed UUID":
				origin, expectedCode = "not-an-environment-id", connect.CodeInvalidArgument
			}
			_, err := f.server.CreateBuild(f.ctx, &buildv1.CreateBuildRequest{
				ResourceId: f.resourceID.String(), DockerfilePath: testDockerfile, SourceSize: 100,
				OriginEnvironmentId: &origin,
			})
			wantCode(t, err, expectedCode)
			if len(f.bucket.presigned) != 0 {
				t.Fatal("invalid origin received an upload URL")
			}
			if f.count(t, `SELECT count(*) FROM builds WHERE resource_id=$1`) != 0 {
				t.Fatal("invalid origin created a build")
			}
		})
	}
}

func TestStandaloneBuildKeepsAnAbsentOrigin(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	id := f.create(t, 100).GetBuildId()
	if stored := f.row(t, id); stored.OriginEnvironmentID != nil {
		t.Fatalf("standalone stored origin = %v, want none", stored.OriginEnvironmentID)
	}
	if f.get(t, id).OriginEnvironmentId != nil {
		t.Fatal("standalone response invented an origin")
	}
}

func TestBuildHistoryFiltersOriginBeforePagination(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	staging := f.addStagingEnvironment(t)
	var all, stagingBuilds, productionBuilds []string
	for _, origin := range []*uuid.UUID{&staging, nil, &f.envID, &staging, nil, &f.envID} {
		request := &buildv1.CreateBuildRequest{
			ResourceId: f.resourceID.String(), DockerfilePath: testDockerfile, SourceSize: 100,
		}
		if origin != nil {
			request.OriginEnvironmentId = new(origin.String())
		}
		created, err := f.server.CreateBuild(f.ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		id := created.GetBuildId()
		all = append([]string{id}, all...)
		if origin != nil && *origin == staging {
			stagingBuilds = append([]string{id}, stagingBuilds...)
		} else if origin != nil {
			productionBuilds = append([]string{id}, productionBuilds...)
		}
	}
	if err := f.queries.DeleteEnvironment(t.Context(), staging); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		origin *string
		want   []string
	}{
		{"unfiltered includes missing origins", nil, all},
		{"deleted staging origin remains filterable", new(staging.String()), stagingBuilds},
		{"production excludes staging and missing origins", new(f.envID.String()), productionBuilds},
		{"unknown origin is empty", new(uuid.NewString()), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			request := &buildv1.ListBuildsRequest{
				ResourceId: f.resourceID.String(), OriginEnvironmentId: tc.origin, PageSize: 1,
			}
			for {
				page, err := f.server.ListBuilds(f.ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				for _, build := range page.GetBuilds() {
					got = append(got, build.GetId())
				}
				if page.GetNextPageToken() == "" {
					break
				}
				request.PageToken = page.GetNextPageToken()
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("builds = %v, want %v", got, tc.want)
			}
		})
	}
}
