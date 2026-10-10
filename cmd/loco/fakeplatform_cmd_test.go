package loco

import (
	"slices"
	"strconv"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const waitTimeout = 15 * time.Second

func cmdFakePlatform(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	switch args[0] {
	case "build-outcome":
		requireArgs(ts, neg, args, 2, "fakeapi build-outcome <succeeded|failed|running>")
		api.mu.Lock()
		api.platform.outcome = args[1]
		api.mu.Unlock()
	case "upload-mode":
		requireArgs(ts, neg, args, 2, "fakeapi upload-mode <ok|forbidden|reset>")
		api.mu.Lock()
		api.platform.uploadMode = args[1]
		api.mu.Unlock()
	case "builds-unavailable":
		requireArgs(ts, neg, args, 2, "fakeapi builds-unavailable <CreateBuild|StartBuild>")
		api.mu.Lock()
		api.platform.buildsUnavailable = args[1]
		api.mu.Unlock()
	case "source-limit":
		requireArgs(ts, neg, args, 2, "fakeapi source-limit <bytes>")
		limit, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			ts.Fatalf("fakeapi: %v", err)
		}
		api.mu.Lock()
		api.platform.sourceLimit = limit
		api.mu.Unlock()
	case "no-proxy":
		requireArgs(ts, neg, args, 1, "fakeapi no-proxy")
		api.mu.Lock()
		api.platform.noProxy = true
		api.mu.Unlock()
	case "seed":
		requireArgs(ts, neg, args, 2, "fakeapi seed <service>")
		seedBuild(api, args[1])
	case "resource":
		requireArgs(ts, neg, args, 2, "fakeapi resource <service>")
		api.mu.Lock()
		api.platform.addResource(args[1])
		api.mu.Unlock()
	case "built":
		checkBuilt(ts, api, neg, args)
	case "uploaded":
		checkUploaded(ts, api, neg, args)
	case "upload-header":
		checkUploadHeader(ts, api, neg, args)
	case "wait":
		requireArgs(ts, neg, args, 2, "fakeapi wait <method>")
		waitForCall(ts, api, args[1])
	case "plan":
		requireArgs(ts, neg, args, 2, "fakeapi plan <plan.json>")
		data := ts.ReadFile(args[1])
		api.mu.Lock()
		defer api.mu.Unlock()
		if err := api.platform.setPlan(data); err != nil {
			ts.Fatalf("fakeapi: %v", err)
		}
	case "planned":
		checkPlanned(ts, api, neg, args)
	case "revision-moves":
		requireArgs(ts, neg, args, 1, "fakeapi revision-moves")
		api.mu.Lock()
		api.platform.revisionMoves = true
		api.mu.Unlock()
	case "images-move":
		requireArgs(ts, neg, args, 1, "fakeapi images-move")
		api.mu.Lock()
		api.platform.imagesMove = true
		api.mu.Unlock()
	case "cluster-held":
		requireArgs(ts, neg, args, 1, "fakeapi cluster-held")
		api.mu.Lock()
		api.platform.clusterHeld = true
		api.mu.Unlock()
	case "applied":
		checkApplied(ts, api, neg, args)
	case "provisioned":
		checkProvisioned(ts, api, neg, args)
	case "partial":
		checkPartial(ts, api, neg, args)
	default:
		ts.Fatalf("fakeapi: unknown subcommand %q", args[0])
	}
}

func requireArgs(ts *testscript.TestScript, neg bool, args []string, n int, usage string) {
	if neg || len(args) != n {
		ts.Fatalf("usage: %s", usage)
	}
}

func seedBuild(api *fakeAPI, name string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	resourceID := "00000000-0000-7000-8000-0000000000a1"
	api.platform.resources = append(api.platform.resources, &resourcev1.Resource{
		Id:          resourceID,
		WorkspaceId: fakeWorkspaceID,
		Name:        name,
	})
	cluster := fakeClusterID
	digest := fakeImageDigest
	deletedDigest := fakeDeletedDigest
	started := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	finished := started.Add(42 * time.Second)
	earlier := started.Add(-time.Hour)
	earlierFinished := earlier.Add(42 * time.Second)
	api.platform.builds = append(api.platform.builds, &buildv1.Build{
		Id:              "0199b6c4-5d1e-7f00-8000-0000000000b0",
		ResourceId:      resourceID,
		ClusterId:       &cluster,
		Status:          buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED,
		SourceType:      sourceTypeUpload,
		SourceSize:      2048,
		DockerfilePath:  fakeDockerfile,
		ImageRepository: fakeImageRepo,
		ImageDigest:     &deletedDigest,
		CreatedAt:       timestamppb.New(earlier),
		StartedAt:       timestamppb.New(earlier),
		FinishedAt:      timestamppb.New(earlierFinished),
		ImageDeletedAt:  timestamppb.New(started),
	}, &buildv1.Build{
		Id:              "0199b6c4-5d1e-7f00-8000-0000000000b1",
		ResourceId:      resourceID,
		ClusterId:       &cluster,
		Status:          buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED,
		SourceType:      sourceTypeUpload,
		SourceSize:      2048,
		DockerfilePath:  fakeDockerfile,
		ImageRepository: fakeImageRepo,
		ImageDigest:     &digest,
		CreatedAt:       timestamppb.New(started),
		StartedAt:       timestamppb.New(started),
		FinishedAt:      timestamppb.New(finished),
	}, &buildv1.Build{
		Id:              "0199b6c4-5d1e-7f00-8000-0000000000b2",
		ResourceId:      resourceID,
		ClusterId:       &cluster,
		Status:          buildv1.BuildStatus_BUILD_STATUS_RUNNING,
		SourceType:      sourceTypeUpload,
		SourceSize:      2048,
		DockerfilePath:  fakeDockerfile,
		ImageRepository: fakeImageRepo,
		CreatedAt:       timestamppb.New(finished),
		StartedAt:       timestamppb.New(finished),
	})
	api.platform.polls["0199b6c4-5d1e-7f00-8000-0000000000b2"] = 1
	api.platform.outcome = outcomeRunning
}

func checkUploaded(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: fakeapi uploaded <archive entry>")
	}
	names, _, err := api.uploadedEntries()
	if err != nil {
		ts.Fatalf("fakeapi: %v", err)
	}
	found := slices.Contains(names, args[1])
	if neg && found {
		ts.Fatalf("fakeapi: the archive contains %s: %v", args[1], names)
	}
	if !neg && !found {
		ts.Fatalf("fakeapi: the archive lacks %s: %v", args[1], names)
	}
}

func checkUploadHeader(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: fakeapi upload-header <Content-Length|Content-Type> <value>")
	}
	_, upload, err := api.uploadedEntries()
	if err != nil {
		ts.Fatalf("fakeapi: %v", err)
	}
	got := upload.contentType
	if args[1] == "Content-Length" {
		got = upload.contentLength
		if args[2] == "body" {
			args[2] = strconv.Itoa(len(upload.body))
		}
	}
	if got != args[2] {
		ts.Fatalf("fakeapi: upload %s = %q, want %q", args[1], got, args[2])
	}
}

func checkPlanned(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: fakeapi planned <loco.yaml>")
	}
	want := ts.ReadFile(args[1])
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.platform.planned) == 0 {
		ts.Fatalf("fakeapi: Plan was not called")
	}
	got := string(api.platform.planned[len(api.platform.planned)-1])
	if got != want {
		ts.Fatalf("fakeapi: Plan received:\n%s\nwant:\n%s", got, want)
	}
}

func checkApplied(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: fakeapi applied <revision>")
	}
	revision, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		ts.Fatalf("fakeapi: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	found := false
	for _, apply := range api.platform.applies {
		if apply.GetRevision() == revision {
			found = true
		}
	}
	if neg && found {
		ts.Fatalf("fakeapi: an apply at revision %d went through", revision)
	}
	if !neg && !found {
		ts.Fatalf("fakeapi: no apply at revision %d in %d applies", revision, len(api.platform.applies))
	}
}

func checkProvisioned(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) < 2 {
		ts.Fatalf("usage: fakeapi provisioned <revision> [service...]")
	}
	revision, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		ts.Fatalf("fakeapi: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	index := slices.IndexFunc(api.platform.applies, func(apply *planv1.ApplyRequest) bool {
		return apply.GetRevision() == revision
	})
	if index < 0 {
		ts.Fatalf("fakeapi: no apply at revision %d in %d applies", revision, len(api.platform.applies))
	}
	got := api.platform.applies[index].GetProvision()
	if want := args[2:]; !slices.Equal(got, want) {
		ts.Fatalf("fakeapi: the apply at revision %d provisioned %v, want %v", revision, got, want)
	}
}

func checkBuilt(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if len(args) != 4 {
		ts.Fatalf("usage: fakeapi built <service> <dockerfile> <context>")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.platform.applies) == 0 {
		ts.Fatalf("fakeapi: Apply was not called")
	}
	last := api.platform.applies[len(api.platform.applies)-1]
	buildID, named := last.GetBuilds()[args[1]]
	if neg {
		if named {
			ts.Fatalf("fakeapi: the last apply named build %s for %s", buildID, args[1])
		}
		return
	}
	build := api.findBuild(buildID)
	if !named || build == nil {
		ts.Fatalf("fakeapi: the last apply named no build for %s: %v", args[1], last.GetBuilds())
	}
	if build.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED ||
		build.GetDockerfilePath() != args[2] || build.GetContext() != args[3] {
		ts.Fatalf("fakeapi: %s deployed build %s (%s, %s/%s), want a succeeded build of %s/%s",
			args[1], buildID, build.GetStatus(), build.GetContext(), build.GetDockerfilePath(), args[3], args[2])
	}
}

func checkPartial(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: fakeapi partial <service> <partial>")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, res := range api.platform.resources {
		if res.GetName() != args[1] {
			continue
		}
		if res.GetPartial() != args[2] {
			ts.Fatalf("fakeapi: %s is in partial %q, want %q", args[1], res.GetPartial(), args[2])
		}
		return
	}
	ts.Fatalf("fakeapi: no resource named %s", args[1])
}

func waitForCall(ts *testscript.TestScript, api *fakeAPI, method string) {
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if api.called(method, "") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	ts.Fatalf("fakeapi: %s was not called within %s", method, waitTimeout)
}
