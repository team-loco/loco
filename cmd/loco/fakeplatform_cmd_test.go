package loco

import (
	"slices"
	"strconv"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
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
	case "uploaded":
		checkUploaded(ts, api, neg, args)
	case "upload-header":
		checkUploadHeader(ts, api, neg, args)
	case "deployed":
		checkDeployed(ts, api, neg, args)
	case "tag-moves":
		requireArgs(ts, neg, args, 1, "fakeapi tag-moves")
		api.mu.Lock()
		api.platform.tagMoves = true
		api.mu.Unlock()
	case "pinned-once":
		checkPinnedOnce(ts, api, neg, args)
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
	case "applied":
		checkApplied(ts, api, neg, args)
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

func checkDeployed(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: fakeapi deployed <region> <dockerfile|image>")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	latestBuild := ""
	if n := len(api.platform.builds); n > 0 {
		latestBuild = api.platform.builds[n-1].GetId()
	}
	for _, d := range api.platform.deployments {
		build := d.GetSpec().GetService().GetBuild()
		if d.GetRegion() != args[1] || build.GetType() != args[2] {
			continue
		}
		if d.GetEnvironmentId() != fakeEnvironmentID {
			ts.Fatalf("fakeapi: deployment to %s used environment %q", args[1], d.GetEnvironmentId())
		}
		if args[2] == "dockerfile" && build.GetBuildId() != latestBuild {
			ts.Fatalf("fakeapi: deployment to %s used build %q, want %q", args[1], build.GetBuildId(), latestBuild)
		}
		return
	}
	ts.Fatalf("fakeapi: no %s deployment to %s in %d deployments", args[2], args[1], len(api.platform.deployments))
}

func checkPinnedOnce(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	requireArgs(ts, neg, args, 1, "fakeapi pinned-once")
	api.mu.Lock()
	defer api.mu.Unlock()
	pinned := api.platform.pinned
	if len(pinned) < 2 {
		ts.Fatalf("fakeapi: %d deployments, want at least 2", len(pinned))
	}
	for i, image := range pinned {
		if image != pinned[0] {
			ts.Fatalf("fakeapi: deployment %d runs %s, the first runs %s", i, image, pinned[0])
		}
	}
	if api.platform.resolutions > 1 {
		ts.Fatalf("fakeapi: the image tag was resolved %d times", api.platform.resolutions)
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
