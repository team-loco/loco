package main

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const waitTimeout = 15 * time.Second

func cmdFakePlatform(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	switch args[0] {
	case "event-page-sizes", "build-page-sizes":
		requireArgs(ts, neg, args, 2, "fakeapi event-page-sizes|build-page-sizes <sizes>")
		api.mu.Lock()
		sizes := api.platform.eventPageSizes
		if args[0] == "build-page-sizes" {
			sizes = api.platform.buildPageSizes
		}
		got := fmt.Sprint(sizes)
		api.mu.Unlock()
		if got != args[1] {
			ts.Fatalf("%s = %s, want %s", args[0], got, args[1])
		}
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
	case "no-platform-domain":
		requireArgs(ts, neg, args, 1, "fakeapi no-platform-domain")
		api.mu.Lock()
		api.platform.noPlatformDomain = true
		api.mu.Unlock()
	case "seed":
		requireArgs(ts, neg, args, 2, "fakeapi seed <service>")
		seedBuild(api, args[1])
	case "resource":
		requireArgs(ts, neg, args, 2, "fakeapi resource <service>")
		api.mu.Lock()
		api.platform.addResource(args[1])
		api.mu.Unlock()
	case "environment":
		requireArgs(ts, neg, args, 2, "fakeapi environment <name>")
		api.mu.Lock()
		api.platform.addEnvironment(args[1])
		api.mu.Unlock()
	case "deployment", "finished", "restarted", "scaled", "canceled":
		cmdFakeDeployments(ts, api, neg, args)
	case "built", "uploaded", "upload-header":
		cmdFakeSources(ts, api, neg, args)
	case "secret", "secret-usage":
		cmdFakeSecrets(ts, api, neg, args)
	case "wait":
		requireArgs(ts, neg, args, 2, "fakeapi wait <method>")
		waitForCall(api, args[1])
	case "plan":
		requireArgs(ts, neg, args, 2, "fakeapi plan <plan.json>")
		data := ts.ReadFile(args[1])
		api.mu.Lock()
		defer api.mu.Unlock()
		if err := api.platform.setPlan(data); err != nil {
			ts.Fatalf("fakeapi: %v", err)
		}
	case "image-override":
		checkImageOverride(ts, api, neg, args)
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

func setDeploymentOutcome(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) < 3 {
		ts.Fatalf("usage: fakeapi deployment <id> <running|failed|canceled|deploying> " +
			"[<region>=<placement status>...] [canceled-by=<name>] [superseded-by=<id>]")
	}
	phase, ok := deploymentv1.DeploymentStatus_value["DEPLOYMENT_STATUS_"+strings.ToUpper(args[2])]
	if !ok {
		ts.Fatalf("fakeapi: unknown deployment phase %q", args[2])
	}
	placements := make([]*deploymentv1.DeploymentPlacement, 0, len(args)-3)
	canceledBy, supersededBy := "", ""
	for _, arg := range args[3:] {
		region, status, _ := strings.Cut(arg, "=")
		switch region {
		case "canceled-by":
			canceledBy = status
			continue
		case "superseded-by":
			supersededBy = status
			continue
		}
		value, known := deploymentv1.PlacementStatus_value["PLACEMENT_STATUS_"+strings.ToUpper(status)]
		if !known {
			ts.Fatalf("fakeapi: unknown placement status %q", status)
		}
		placements = append(placements, &deploymentv1.DeploymentPlacement{
			Region: region, Status: deploymentv1.PlacementStatus(value),
		})
	}
	if supersededBy != "" {
		placements = append(placements, &deploymentv1.DeploymentPlacement{
			Region:       fakeDefaultRegion,
			Status:       deploymentv1.PlacementStatus_PLACEMENT_STATUS_CANCELED,
			SupersededBy: supersededBy,
		})
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.platform.deploymentOutcomes[args[1]] = deploymentv1.DeploymentStatus(phase)
	api.platform.deploymentPlacements[args[1]] = placements
	api.platform.deploymentCanceledBy[args[1]] = canceledBy
}

// cmdFakeDeployments sets deployment outcomes and finished regions, or checks the restart,
// scale or cancel the fake recorded.
func cmdFakeDeployments(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	switch args[0] {
	case "deployment":
		setDeploymentOutcome(ts, api, neg, args)
	case "finished":
		setFinishedRegions(ts, api, neg, args)
	case "restarted":
		checkRestarted(ts, api, neg, args)
	case "scaled":
		checkScaled(ts, api, neg, args)
	default:
		checkCanceled(ts, api, neg, args)
	}
}

// cmdFakeSources checks the builds and source uploads the fake recorded.
func cmdFakeSources(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	switch args[0] {
	case "built":
		checkBuilt(ts, api, neg, args)
	case "uploaded":
		checkUploaded(ts, api, neg, args)
	default:
		checkUploadHeader(ts, api, neg, args)
	}
}

func checkCanceled(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) < 2 || len(args) > 3 || len(args) == 3 && args[2] != "rollback" && args[2] != "no-rollback" {
		ts.Fatalf("usage: fakeapi canceled <deployment id> [rollback|no-rollback]")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if !slices.Contains(api.platform.canceledDeployments, args[1]) {
		ts.Fatalf("fakeapi: %s was not canceled; canceled: %v", args[1], api.platform.canceledDeployments)
	}
	if len(args) == 3 && api.platform.canceledRollbacks[args[1]] != (args[2] == "rollback") {
		ts.Fatalf("fakeapi: cancel of %s asked rollback=%t, want %s",
			args[1], api.platform.canceledRollbacks[args[1]], args[2])
	}
}

// setFinishedRegions makes the deployment already run in each region, after the named earlier
// deployment or none.
func setFinishedRegions(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) < 3 {
		ts.Fatalf("usage: fakeapi finished <deployment id> <region>=<previous deployment id|none>...")
	}
	regions := make([]*deploymentv1.FinishedRegion, 0, len(args)-2)
	for _, arg := range args[2:] {
		region, previous, _ := strings.Cut(arg, "=")
		finished := &deploymentv1.FinishedRegion{Region: region}
		if previous != "none" {
			finished.Previous = &deploymentv1.Deployment{
				Id:        previous,
				StartedAt: timestamppb.New(time.Date(2026, 11, 18, 14, 2, 0, 0, time.UTC)),
				Commit:    &deploymentv1.SourceCommit{Sha: strings.Repeat("a", 40)},
				Spec: &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{
					Service: &deploymentv1.ServiceDeploymentSpec{Build: &deploymentv1.BuildSource{
						Image: "registry.example.test/web:" + previous,
					}},
				}},
			}
		}
		regions = append(regions, finished)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.platform.finishedRegions[args[1]] = regions
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
		Slug:        name,
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
	if build.GetStatus() == buildv1.BuildStatus_BUILD_STATUS_FAILED ||
		build.GetStatus() == buildv1.BuildStatus_BUILD_STATUS_CANCELED ||
		build.GetDockerfilePath() != args[2] ||
		build.GetContext() != args[3] {
		ts.Fatalf("fakeapi: %s deployed build %s (%s, %s/%s), want a deployable build of %s/%s",
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
		if res.GetSlug() != args[1] {
			continue
		}
		if res.GetPartial() != args[2] {
			ts.Fatalf("fakeapi: %s is in partial %q, want %q", args[1], res.GetPartial(), args[2])
		}
		return
	}
	ts.Fatalf("fakeapi: no resource named %s", args[1])
}

func checkRestarted(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) != 4 {
		ts.Fatalf("usage: fakeapi restarted <service> <environment> <region>")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	resourceID, ok := api.platform.resourceID(args[1])
	if !ok {
		ts.Fatalf("fakeapi: no resource named %s", args[1])
	}
	environmentID, ok := api.platform.environmentID(args[2])
	if !ok {
		ts.Fatalf("fakeapi: no environment named %s", args[2])
	}
	for _, restart := range api.platform.restarts {
		if restart.GetResourceId() == resourceID && restart.GetEnvironmentId() == environmentID &&
			restart.GetRegion() == args[3] {
			return
		}
	}
	ts.Fatalf("fakeapi: no matching restart: %v", api.platform.restarts)
}

func checkScaled(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if neg || len(args) < 4 || len(args) > 5 {
		ts.Fatalf("usage: fakeapi scaled <service> <environment> <replicas> [region]")
	}
	region := ""
	if len(args) == 5 {
		region = args[4]
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	resourceID, ok := api.platform.resourceID(args[1])
	if !ok {
		ts.Fatalf("fakeapi: no resource named %s", args[1])
	}
	environmentID, ok := api.platform.environmentID(args[2])
	if !ok {
		ts.Fatalf("fakeapi: no environment named %s", args[2])
	}
	replicas, err := strconv.ParseInt(args[3], 10, 32)
	if err != nil {
		ts.Fatalf("fakeapi: %v", err)
	}
	for _, scale := range api.platform.scales {
		sameTarget := scale.GetResourceId() == resourceID && scale.GetEnvironmentId() == environmentID &&
			scale.GetRegion() == region
		if sameTarget && int64(scale.GetReplicas()) == replicas {
			return
		}
	}
	ts.Fatalf("fakeapi: %s was not scaled to %d replicas in %s; scales: %v",
		args[1], replicas, args[2], api.platform.scales)
}

func waitForCall(api *fakeAPI, method string) {
	for {
		api.mu.Lock()
		recorded := api.recorded
		api.mu.Unlock()
		if api.called(method, "") {
			return
		}
		<-recorded
	}
}

func checkImageOverride(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	requireArgs(ts, neg, args, 3, "fakeapi image-override <service> <image>")
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.platform.applies) == 0 || len(api.platform.planned) == 0 {
		ts.Fatalf("missing plan or apply")
	}
	planned := api.platform.planned[len(api.platform.planned)-1]
	applied := api.platform.applies[len(api.platform.applies)-1]
	if !bytes.Equal(planned, applied.GetFile()) {
		ts.Fatalf("plan and apply received different files")
	}
	if len(applied.GetBuilds()) != 0 {
		ts.Fatalf("image override supplied source builds")
	}
	file, err := locofile.Parse(planned)
	if err != nil {
		ts.Fatalf("%v", err)
	}
	services, err := locofile.Resolve(file, "production")
	if err != nil {
		ts.Fatalf("%v", err)
	}
	service := services[args[1]]
	if service.Image != args[2] || service.Dockerfile != "" || service.Context != "" {
		ts.Fatalf("overridden service = %+v", service)
	}
}

// setFakeReplicas reports one region of the service running a serving replica and one that was
// OOM-killed, as a silent cluster last reported them ten minutes ago when silent is set.
func setFakeReplicas(platform *fakePlatform, service string, silent bool) {
	now := time.Now()
	region := &resourcev1.RegionStatus{
		Region: fakeDefaultRegion, Status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		ReadyReplicas: 1, DesiredReplicas: 2, ReportedAt: timestamppb.New(now), MaxRestarts: 5,
		Replicas: []*deploymentv1.Replica{
			{
				Name: service + "-7d9f-a1", Node: "node-1", State: "Serving", Ready: true,
				StartedAt: timestamppb.New(now.Add(-3 * time.Hour)),
			},
			{
				Name: service + "-7d9f-b2", Node: "node-2", State: "Failing", Restarts: 4,
				StartedAt: timestamppb.New(now.Add(-20 * time.Minute)),
				RestartsAt: []*timestamppb.Timestamp{
					timestamppb.New(now.Add(-8 * time.Minute)), timestamppb.New(now.Add(-5 * time.Minute)),
				},
				LastExit: &deploymentv1.ReplicaExit{
					Reason: "OOMKilled", ExitCode: 137, FinishedAt: timestamppb.New(now.Add(-5 * time.Minute)),
				},
			},
		},
	}
	if silent {
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
		region.ReportedAt = timestamppb.New(now.Add(-10 * time.Minute))
		region.Message = new("the cluster running this region has stopped reporting")
	}
	for _, res := range platform.resources {
		if res.GetSlug() == service {
			res.EnvironmentStatuses = []*resourcev1.EnvironmentStatus{
				{
					EnvironmentSlug: fakeEnvironmentName,
					Status:          region.GetStatus(),
					StatusSince:     timestamppb.New(now.Add(-2 * time.Hour)),
					Regions:         []*resourcev1.RegionStatus{region},
				},
			}
		}
	}
}
