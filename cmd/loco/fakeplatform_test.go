package loco

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	fakeWorkspaceID      = "2"
	fakeEnvironmentID    = "00000000-0000-7000-8000-0000000000e1"
	fakeClusterID        = "00000000-0000-7000-8000-0000000000c1"
	fakeImageRepo        = "registry.loco.test/ws/app"
	fakeImageDigest      = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	fakeDeletedDigest    = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	fakeDockerfile       = "Dockerfile"
	fakePlatformDomainID = "00000000-0000-7000-8000-0000000000d1"
	fakePlatformDomain   = "onloco.test"
	fakeDefaultRegion    = "us-east-1"
	fakeSecondaryRegion  = "eu-west-1"
	uploadModeOK         = "ok"
	uploadModeDenied     = "forbidden"
	uploadModeReset      = "reset"
	outcomeSucceeded     = "succeeded"
	outcomeFailed        = "failed"
	outcomeRunning       = "running"
	uploadPathPrefix     = "/upload/"
	sourceTypeUpload     = "upload"
	sourceTypeImage      = "image"
)

var (
	errFakeUnknownPlatformDomain = errors.New("fakeapi: unknown platform domain")
	errFakeResourceNotFound      = errors.New("resource not found")
)

type fakeUpload struct {
	body          []byte
	contentLength string
	contentType   string
}

type fakePlatform struct {
	baseURL           string
	resources         []*resourcev1.Resource
	environments      []*environmentv1.Environment
	builds            []*buildv1.Build
	polls             map[string]int
	outcome           string
	uploadMode        string
	sourceLimit       int64
	buildsUnavailable string
	uploads           map[string]fakeUpload
	deployments       []*deploymentv1.CreateDeploymentRequest
	pinned            []string
	tagMoves          bool
	resolutions       int
	noProxy           bool
	logQueries        []*observabilityv1.QueryLogsRequest
	plan              *planv1.PlanResponse
	planned           [][]byte
	applies           []*planv1.ApplyRequest
	revisionMoves     bool
	imagesMove        bool
}

func newFakePlatform() *fakePlatform {
	return &fakePlatform{
		environments: []*environmentv1.Environment{{
			Id:          fakeEnvironmentID,
			WorkspaceId: fakeWorkspaceID,
			Name:        "production",
			Type:        environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION,
		}},
		polls:       map[string]int{},
		outcome:     outcomeSucceeded,
		uploadMode:  uploadModeOK,
		sourceLimit: 200 << 20,
		uploads:     map[string]fakeUpload{},
	}
}

func (f *fakeAPI) registerPlatform(mux *http.ServeMux) {
	mux.Handle(resourcev1connect.NewResourceServiceHandler(&fakeResourceService{api: f}))
	mux.Handle(domainv1connect.NewDomainServiceHandler(&fakeDomainService{api: f}))
	mux.Handle(environmentv1connect.NewEnvironmentServiceHandler(&fakeEnvironmentService{api: f}))
	mux.Handle(buildv1connect.NewBuildServiceHandler(&fakeBuildService{api: f}))
	mux.Handle(deploymentv1connect.NewDeploymentServiceHandler(&fakeDeploymentService{api: f}))
	mux.Handle(observabilityv1connect.NewObservabilityAccessServiceHandler(&fakeAccessService{api: f}))
	mux.Handle(observabilityv1connect.NewObservabilityProxyServiceHandler(&fakeProxyService{api: f}))
	mux.HandleFunc(uploadPathPrefix, f.handleUpload)
	f.registerPlan(mux)
}

func (f *fakeAPI) findBuild(id string) *buildv1.Build {
	for _, b := range f.platform.builds {
		if b.GetId() == id {
			return b
		}
	}
	return nil
}

func (f *fakeAPI) handleUpload(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	mode := f.platform.uploadMode
	f.calls = append(f.calls, apiCall{method: "Upload"})
	f.mu.Unlock()

	switch mode {
	case uploadModeDenied:
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<Error><Code>SignatureDoesNotMatch</Code></Error>")
		return
	case uploadModeReset:
		resetConnection(w)
		return
	default:
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, uploadPathPrefix)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.platform.uploads[id] = fakeUpload{
		body:          body,
		contentLength: r.Header.Get("Content-Length"),
		contentType:   r.Header.Get("Content-Type"),
	}
	w.WriteHeader(http.StatusOK)
}

func resetConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if tcp, isTCP := conn.(*net.TCPConn); isTCP {
		if lingerErr := tcp.SetLinger(0); lingerErr != nil {
			return
		}
	}
	if closeErr := conn.Close(); closeErr != nil {
		return
	}
}

func (f *fakeAPI) uploadedEntries() ([]string, fakeUpload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.platform.builds) == 0 {
		return nil, fakeUpload{}, errors.New("no build was created")
	}
	latest := f.platform.builds[len(f.platform.builds)-1].GetId()
	upload, ok := f.platform.uploads[latest]
	if !ok {
		return nil, fakeUpload{}, fmt.Errorf("build %s has no upload", latest)
	}
	gz, err := gzip.NewReader(bytes.NewReader(upload.body))
	if err != nil {
		return nil, upload, err
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			return names, upload, nil
		}
		if nextErr != nil {
			return nil, upload, nextErr
		}
		names = append(names, hdr.Name)
	}
}

type fakeResourceService struct {
	resourcev1connect.UnimplementedResourceServiceHandler

	api *fakeAPI
}

func (s *fakeResourceService) GetResource(
	_ context.Context,
	req *connect.Request[resourcev1.GetResourceRequest],
) (*connect.Response[resourcev1.GetResourceResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, res := range f.platform.resources {
		byName := req.Msg.GetNameKey().GetName() != "" && res.GetName() == req.Msg.GetNameKey().GetName()
		byID := req.Msg.GetResourceId() != "" && res.GetId() == req.Msg.GetResourceId()
		if byName || byID {
			return connect.NewResponse(&resourcev1.GetResourceResponse{Resource: res}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errFakeResourceNotFound)
}

func (s *fakeResourceService) CreateResource(
	_ context.Context,
	req *connect.Request[resourcev1.CreateResourceRequest],
) (*connect.Response[resourcev1.CreateResourceResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("00000000-0000-7000-8000-%012d", len(f.platform.resources)+1)
	resource := &resourcev1.Resource{
		Id:          id,
		WorkspaceId: req.Msg.GetWorkspaceId(),
		Name:        req.Msg.GetName(),
	}
	if input := req.Msg.GetDomain(); input != nil {
		if input.GetPlatformDomainId() != fakePlatformDomainID {
			return nil, connect.NewError(connect.CodeInvalidArgument, errFakeUnknownPlatformDomain)
		}
		hostname := input.GetSubdomain() + "." + fakePlatformDomain
		resource.Domains = []*domainv1.ResourceDomain{{ResourceId: id, Domain: hostname, IsPrimary: true}}
	}
	f.platform.resources = append(f.platform.resources, resource)
	return connect.NewResponse(&resourcev1.CreateResourceResponse{ResourceId: id}), nil
}

func (s *fakeResourceService) ListRegions(
	_ context.Context,
	req *connect.Request[resourcev1.ListRegionsRequest],
) (*connect.Response[resourcev1.ListRegionsResponse], error) {
	if _, _, err := s.api.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	regions := []*resourcev1.RegionInfo{
		{Region: fakeSecondaryRegion},
		{Region: fakeDefaultRegion, IsDefault: true},
	}
	return connect.NewResponse(&resourcev1.ListRegionsResponse{Regions: regions}), nil
}

func (s *fakeResourceService) GetResourceStatus(
	_ context.Context,
	req *connect.Request[resourcev1.GetResourceStatusRequest],
) (*connect.Response[resourcev1.GetResourceStatusResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, res := range f.platform.resources {
		if res.GetId() == req.Msg.GetResourceId() {
			return connect.NewResponse(&resourcev1.GetResourceStatusResponse{Resource: res}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errFakeResourceNotFound)
}

func (s *fakeResourceService) TransferPartial(
	_ context.Context,
	req *connect.Request[resourcev1.TransferPartialRequest],
) (*connect.Response[resourcev1.TransferPartialResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, res := range f.platform.resources {
		if res.GetId() == req.Msg.GetResourceId() {
			partial := req.Msg.GetPartial()
			res.Partial = &partial
			return connect.NewResponse(&resourcev1.TransferPartialResponse{}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errFakeResourceNotFound)
}

type fakeDomainService struct {
	domainv1connect.UnimplementedDomainServiceHandler

	api *fakeAPI
}

func (s *fakeDomainService) ListPlatformDomains(
	_ context.Context,
	req *connect.Request[domainv1.ListPlatformDomainsRequest],
) (*connect.Response[domainv1.ListPlatformDomainsResponse], error) {
	if _, _, err := s.api.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	platformDomain := &domainv1.PlatformDomain{Id: fakePlatformDomainID, Domain: fakePlatformDomain, IsActive: true}
	resp := &domainv1.ListPlatformDomainsResponse{PlatformDomains: []*domainv1.PlatformDomain{platformDomain}}
	return connect.NewResponse(resp), nil
}

type fakeEnvironmentService struct {
	environmentv1connect.UnimplementedEnvironmentServiceHandler

	api *fakeAPI
}

func (s *fakeEnvironmentService) ListEnvironments(
	_ context.Context,
	req *connect.Request[environmentv1.ListEnvironmentsRequest],
) (*connect.Response[environmentv1.ListEnvironmentsResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return connect.NewResponse(&environmentv1.ListEnvironmentsResponse{Environments: f.platform.environments}), nil
}

type fakeBuildService struct {
	buildv1connect.UnimplementedBuildServiceHandler

	api *fakeAPI
}

func (s *fakeBuildService) CreateBuild(
	_ context.Context,
	req *connect.Request[buildv1.CreateBuildRequest],
) (*connect.Response[buildv1.CreateBuildResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.platform.buildsUnavailable == "CreateBuild" {
		return nil, fakeBuildsUnavailable()
	}
	size := req.Msg.GetSourceSize()
	if size > f.platform.sourceLimit {
		tooLarge := fmt.Errorf("source is %d bytes, over the %d byte limit", size, f.platform.sourceLimit)
		return nil, connect.NewError(connect.CodeInvalidArgument, tooLarge)
	}
	id := fmt.Sprintf("0199b6c4-5d1e-7f00-8000-%012d", len(f.platform.builds)+1)
	f.platform.builds = append(f.platform.builds, &buildv1.Build{
		Id:              id,
		ResourceId:      req.Msg.GetResourceId(),
		Status:          buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD,
		SourceType:      sourceTypeUpload,
		SourceSize:      size,
		DockerfilePath:  req.Msg.GetDockerfilePath(),
		ImageRepository: fakeImageRepo,
		CreatedAt:       timestamppb.Now(),
	})
	return connect.NewResponse(&buildv1.CreateBuildResponse{
		BuildId:   id,
		UploadUrl: f.platform.baseURL + uploadPathPrefix + id,
	}), nil
}

func (s *fakeBuildService) StartBuild(
	_ context.Context,
	req *connect.Request[buildv1.StartBuildRequest],
) (*connect.Response[buildv1.StartBuildResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.platform.buildsUnavailable == "StartBuild" {
		return nil, fakeBuildsUnavailable()
	}
	build := f.findBuild(req.Msg.GetBuildId())
	if build == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("build not found"))
	}
	if _, ok := f.platform.uploads[build.GetId()]; !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("source not uploaded"))
	}
	build.Status = buildv1.BuildStatus_BUILD_STATUS_QUEUED
	return connect.NewResponse(&buildv1.StartBuildResponse{Build: build}), nil
}

func fakeBuildsUnavailable() *connect.Error {
	reason := errors.New("no cluster in this install accepts builds")
	connectErr := connect.NewError(connect.CodeFailedPrecondition, reason)
	detail, err := connect.NewErrorDetail(&buildv1.BuildsUnavailable{})
	if err != nil {
		panic(err)
	}
	connectErr.AddDetail(detail)
	return connectErr
}

func (f *fakeAPI) advance(build *buildv1.Build) {
	f.platform.polls[build.GetId()]++
	if f.platform.polls[build.GetId()] == 1 {
		cluster := fakeClusterID
		build.ClusterId = &cluster
		build.StartedAt = timestamppb.Now()
		build.Status = buildv1.BuildStatus_BUILD_STATUS_RUNNING
		return
	}
	switch f.platform.outcome {
	case outcomeSucceeded:
		digest := fakeImageDigest
		build.ImageDigest = &digest
		build.Status = buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED
		build.FinishedAt = timestamppb.Now()
	case outcomeFailed:
		build.Status = buildv1.BuildStatus_BUILD_STATUS_FAILED
		build.Message = "RUN go build exited with code 1"
		build.FinishedAt = timestamppb.Now()
	default:
	}
}

func (s *fakeBuildService) GetBuild(
	_ context.Context,
	req *connect.Request[buildv1.GetBuildRequest],
) (*connect.Response[buildv1.GetBuildResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	build := f.findBuild(req.Msg.GetBuildId())
	if build == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("build not found"))
	}
	if build.GetStatus() == buildv1.BuildStatus_BUILD_STATUS_QUEUED ||
		build.GetStatus() == buildv1.BuildStatus_BUILD_STATUS_RUNNING {
		f.advance(build)
	}
	return connect.NewResponse(&buildv1.GetBuildResponse{Build: build}), nil
}

func (s *fakeBuildService) ListBuilds(
	_ context.Context,
	req *connect.Request[buildv1.ListBuildsRequest],
) (*connect.Response[buildv1.ListBuildsResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var builds []*buildv1.Build
	for _, b := range slices.Backward(f.platform.builds) {
		if b.GetResourceId() == req.Msg.GetResourceId() {
			builds = append(builds, b)
		}
	}
	return connect.NewResponse(&buildv1.ListBuildsResponse{Builds: builds}), nil
}

func (s *fakeBuildService) CancelBuild(
	_ context.Context,
	req *connect.Request[buildv1.CancelBuildRequest],
) (*connect.Response[buildv1.CancelBuildResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	build := f.findBuild(req.Msg.GetBuildId())
	if build == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("build not found"))
	}
	build.Status = buildv1.BuildStatus_BUILD_STATUS_CANCELED
	build.Message = "canceled by a user"
	build.FinishedAt = timestamppb.Now()
	return connect.NewResponse(&buildv1.CancelBuildResponse{Build: build}), nil
}

type fakeDeploymentService struct {
	deploymentv1connect.UnimplementedDeploymentServiceHandler

	api *fakeAPI
}

func (s *fakeDeploymentService) CreateDeployment(
	_ context.Context,
	req *connect.Request[deploymentv1.CreateDeploymentRequest],
) (*connect.Response[deploymentv1.CreateDeploymentResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.platform.deployments = append(f.platform.deployments, req.Msg)
	requested := req.Msg.GetSpec().GetService().GetBuild()
	pinned := f.platform.pin(requested)
	f.platform.pinned = append(f.platform.pinned, pinned.GetImage())
	id := "dep-" + req.Msg.GetRegion()
	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{DeploymentId: id, Build: pinned}), nil
}

func (p *fakePlatform) pin(requested *deploymentv1.BuildSource) *deploymentv1.BuildSource {
	if requested.GetType() != sourceTypeImage {
		image := fakeImageRepo + "@" + fakeImageDigest
		buildID := requested.GetBuildId()
		return &deploymentv1.BuildSource{Type: requested.GetType(), Image: image, BuildId: &buildID}
	}
	image := requested.GetImage()
	if strings.Contains(image, "@") {
		return &deploymentv1.BuildSource{Type: sourceTypeImage, Image: image}
	}
	repository, _, _ := strings.Cut(image, ":")
	digest := fakeImageDigest
	if p.tagMoves {
		p.resolutions++
		digest = fmt.Sprintf("sha256:%064d", p.resolutions)
	}
	return &deploymentv1.BuildSource{Type: sourceTypeImage, Image: repository + "@" + digest}
}

func (s *fakeDeploymentService) WatchDeployment(
	_ context.Context,
	req *connect.Request[deploymentv1.WatchDeploymentRequest],
	stream *connect.ServerStream[deploymentv1.WatchDeploymentResponse],
) error {
	if _, _, err := s.api.authenticate(req.Spec(), req.Header()); err != nil {
		return err
	}
	phases := []deploymentv1.DeploymentPhase{
		deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING,
		deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	}
	for _, phase := range phases {
		event := &deploymentv1.WatchDeploymentResponse{
			DeploymentId: req.Msg.GetDeploymentId(),
			Status:       phase,
			Message:      "replicas " + phase.String(),
		}
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

type fakeAccessService struct {
	observabilityv1connect.UnimplementedObservabilityAccessServiceHandler

	api *fakeAPI
}

func (s *fakeAccessService) GetObservabilityAccess(
	_ context.Context,
	req *connect.Request[observabilityv1.GetObservabilityAccessRequest],
) (*connect.Response[observabilityv1.GetObservabilityAccessResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &observabilityv1.GetObservabilityAccessResponse{}
	if !f.platform.noProxy {
		resp.Clusters = []*observabilityv1.ClusterAccess{{
			ClusterId: fakeClusterID,
			ProxyUrl:  f.platform.baseURL,
			Region:    "us-east-1",
		}}
	}
	return connect.NewResponse(resp), nil
}

type fakeProxyService struct {
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler

	api *fakeAPI
}

func fakeLogLines(labels map[string]string, resourceIDs []string) []string {
	if buildID := labels["loco.io/build-id"]; buildID != "" {
		return []string{"#1 [internal] load build definition from Dockerfile", "#5 DONE " + buildID}
	}
	if len(resourceIDs) > 0 {
		return []string{"listening on :8080", "GET /healthz 200"}
	}
	return nil
}

func (s *fakeProxyService) QueryLogs(
	_ context.Context,
	req *connect.Request[observabilityv1.QueryLogsRequest],
) (*connect.Response[observabilityv1.QueryLogsResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.platform.logQueries = append(f.platform.logQueries, req.Msg)
	f.mu.Unlock()
	base := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	lines := fakeLogLines(req.Msg.GetLabels(), req.Msg.GetResourceIds())
	entries := make([]*observabilityv1.LogEntry, 0, len(lines))
	for i, line := range lines {
		ts := base.Add(time.Duration(i) * time.Second)
		entries = append(entries, &observabilityv1.LogEntry{Timestamp: timestamppb.New(ts), Body: line})
	}
	if req.Msg.GetOrder() == observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST {
		slices.Reverse(entries)
	}
	return connect.NewResponse(&observabilityv1.QueryLogsResponse{Entries: entries}), nil
}

func (s *fakeProxyService) TailLogs(
	ctx context.Context,
	req *connect.Request[observabilityv1.TailLogsRequest],
	stream *connect.ServerStream[observabilityv1.TailLogsResponse],
) error {
	if _, _, err := s.api.authenticate(req.Spec(), req.Header()); err != nil {
		return err
	}
	entry := &observabilityv1.LogEntry{Timestamp: timestamppb.Now(), Body: "tailed line"}
	resp := &observabilityv1.TailLogsResponse{Event: &observabilityv1.TailLogsResponse_Entry{Entry: entry}}
	if err := stream.Send(resp); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
