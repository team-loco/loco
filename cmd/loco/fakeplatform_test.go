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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/internal/rpcerr"
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
	errFakeEnvironmentNotFound   = errors.New("environment not found")
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
	noProxy           bool
	logQueries        []*observabilityv1.QueryLogsRequest
	plan              *planv1.PlanResponse
	planned           [][]byte
	applies           []*planv1.ApplyRequest
	revisionMoves     bool
	imagesMove        bool
	clusterHeld       bool
	noPlatformDomain  bool
	secrets           map[fakeSecretKey]fakeSecret
	revision          int64
	scales            []*resourcev1.ScaleResourceRequest
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
		secrets:     map[fakeSecretKey]fakeSecret{},
	}
}

func (f *fakeAPI) registerPlatform(mux *http.ServeMux) {
	server := connect.NewServer()
	resourcev1connect.RegisterResourceServiceHandler(server, &fakeResourceService{api: f})
	domainv1connect.RegisterDomainServiceHandler(server, &fakeDomainService{api: f})
	environmentv1connect.RegisterEnvironmentServiceHandler(server, &fakeEnvironmentService{api: f})
	buildv1connect.RegisterBuildServiceHandler(server, &fakeBuildService{api: f})
	observabilityv1connect.RegisterObservabilityAccessServiceHandler(server, &fakeAccessService{api: f})
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(server, &fakeProxyService{api: f})
	secretv1connect.RegisterSecretServiceHandler(server, &fakeSecretService{api: f})
	connecthttp.Mount(mux, server)

	mux.HandleFunc(uploadPathPrefix, f.handleUpload)
	f.registerPlan(mux)
}

func (p *fakePlatform) addResource(name string) {
	for _, res := range p.resources {
		if res.GetName() == name {
			return
		}
	}
	id := fmt.Sprintf("00000000-0000-7000-8000-%012d", len(p.resources)+1)
	res := &resourcev1.Resource{Id: id, WorkspaceId: fakeWorkspaceID, Name: name}
	p.resources = append(p.resources, res)
}

func (p *fakePlatform) addEnvironment(name string) {
	id := fmt.Sprintf("00000000-0000-7000-8000-0000000e%04d", len(p.environments)+1)
	env := &environmentv1.Environment{Id: id, WorkspaceId: fakeWorkspaceID, Name: name}
	p.environments = append(p.environments, env)
}

func (p *fakePlatform) environmentID(name string) (string, bool) {
	for _, env := range p.environments {
		if env.GetName() == name {
			return env.GetId(), true
		}
	}
	return "", false
}

func (p *fakePlatform) resourceID(name string) (string, bool) {
	for _, res := range p.resources {
		if res.GetName() == name {
			return res.GetId(), true
		}
	}
	return "", false
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
	ctx context.Context,
	req *resourcev1.GetResourceRequest,
) (*resourcev1.GetResourceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: resourcev1connect.ResourceServiceGetResourceProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, res := range f.platform.resources {
		byName := req.GetNameKey().GetName() != "" && res.GetName() == req.GetNameKey().GetName()
		byID := req.GetResourceId() != "" && res.GetId() == req.GetResourceId()
		if byName || byID {
			return &resourcev1.GetResourceResponse{Resource: res}, nil
		}
	}
	return nil, rpcerr.New(connect.CodeNotFound, errFakeResourceNotFound)
}

func (s *fakeResourceService) CreateResource(
	ctx context.Context,
	req *resourcev1.CreateResourceRequest,
) (*resourcev1.CreateResourceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: resourcev1connect.ResourceServiceCreateResourceProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("00000000-0000-7000-8000-%012d", len(f.platform.resources)+1)
	resource := &resourcev1.Resource{
		Id:          id,
		WorkspaceId: req.GetWorkspaceId(),
		Name:        req.GetName(),
	}
	if input := req.GetDomain(); input != nil {
		if input.GetPlatformDomainId() != fakePlatformDomainID {
			return nil, rpcerr.New(connect.CodeInvalidArgument, errFakeUnknownPlatformDomain)
		}
		hostname := input.GetSubdomain() + "." + fakePlatformDomain
		resource.Domains = []*domainv1.ResourceDomain{{ResourceId: id, Domain: hostname, IsPrimary: true}}
	}
	f.platform.resources = append(f.platform.resources, resource)
	return &resourcev1.CreateResourceResponse{ResourceId: id}, nil
}

func (s *fakeResourceService) ListRegions(
	ctx context.Context,
	_ *resourcev1.ListRegionsRequest,
) (*resourcev1.ListRegionsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if _, _, err := s.api.authenticate(
		connect.Spec{Procedure: resourcev1connect.ResourceServiceListRegionsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	regions := []*resourcev1.RegionInfo{
		{Region: fakeSecondaryRegion},
		{Region: fakeDefaultRegion, IsDefault: true},
	}
	return &resourcev1.ListRegionsResponse{Regions: regions}, nil
}

func (s *fakeResourceService) TransferPartial(
	ctx context.Context,
	req *resourcev1.TransferPartialRequest,
) (*resourcev1.TransferPartialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: resourcev1connect.ResourceServiceTransferPartialProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, res := range f.platform.resources {
		if res.GetId() == req.GetResourceId() {
			partial := req.GetPartial()
			res.Partial = &partial
			return &resourcev1.TransferPartialResponse{}, nil
		}
	}
	return nil, rpcerr.New(connect.CodeNotFound, errFakeResourceNotFound)
}

func (s *fakeResourceService) ScaleResource(
	ctx context.Context,
	req *resourcev1.ScaleResourceRequest,
) (*resourcev1.ScaleResourceResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: resourcev1connect.ResourceServiceScaleResourceProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	knownEnvironment := slices.ContainsFunc(f.platform.environments, func(env *environmentv1.Environment) bool {
		return env.GetId() == req.GetEnvironmentId()
	})
	if !knownEnvironment {
		return nil, rpcerr.New(connect.CodeNotFound, errFakeEnvironmentNotFound)
	}
	f.platform.scales = append(f.platform.scales, req)
	return &resourcev1.ScaleResourceResponse{}, nil
}

type fakeDomainService struct {
	domainv1connect.UnimplementedDomainServiceHandler

	api *fakeAPI
}

func (s *fakeDomainService) ListPlatformDomains(
	ctx context.Context,
	_ *domainv1.ListPlatformDomainsRequest,
) (*domainv1.ListPlatformDomainsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if _, _, err := s.api.authenticate(
		connect.Spec{Procedure: domainv1connect.DomainServiceListPlatformDomainsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	platformDomain := &domainv1.PlatformDomain{Id: fakePlatformDomainID, Domain: fakePlatformDomain, IsActive: true}
	resp := &domainv1.ListPlatformDomainsResponse{PlatformDomains: []*domainv1.PlatformDomain{platformDomain}}
	return resp, nil
}

type fakeEnvironmentService struct {
	environmentv1connect.UnimplementedEnvironmentServiceHandler

	api *fakeAPI
}

func (s *fakeEnvironmentService) ListEnvironments(
	ctx context.Context,
	_ *environmentv1.ListEnvironmentsRequest,
) (*environmentv1.ListEnvironmentsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: environmentv1connect.EnvironmentServiceListEnvironmentsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &environmentv1.ListEnvironmentsResponse{Environments: f.platform.environments}, nil
}

type fakeBuildService struct {
	buildv1connect.UnimplementedBuildServiceHandler

	api *fakeAPI
}

func (s *fakeBuildService) CreateBuild(
	ctx context.Context,
	req *buildv1.CreateBuildRequest,
) (*buildv1.CreateBuildResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: buildv1connect.BuildServiceCreateBuildProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.platform.buildsUnavailable == "CreateBuild" {
		return nil, fakeBuildsUnavailable()
	}
	size := req.GetSourceSize()
	if size > f.platform.sourceLimit {
		tooLarge := fmt.Errorf("source is %d bytes, over the %d byte limit", size, f.platform.sourceLimit)
		return nil, rpcerr.New(connect.CodeInvalidArgument, tooLarge)
	}
	id := fmt.Sprintf("0199b6c4-5d1e-7f00-8000-%012d", len(f.platform.builds)+1)
	f.platform.builds = append(f.platform.builds, &buildv1.Build{
		Id:              id,
		ResourceId:      req.GetResourceId(),
		Status:          buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD,
		SourceType:      sourceTypeUpload,
		SourceSize:      size,
		DockerfilePath:  req.GetDockerfilePath(),
		Context:         req.GetContext(),
		ImageRepository: fakeImageRepo,
		CreatedAt:       timestamppb.Now(),
	})
	return &buildv1.CreateBuildResponse{
		BuildId:   id,
		UploadUrl: f.platform.baseURL + uploadPathPrefix + id,
	}, nil
}

func (s *fakeBuildService) StartBuild(
	ctx context.Context,
	req *buildv1.StartBuildRequest,
) (*buildv1.StartBuildResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: buildv1connect.BuildServiceStartBuildProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.platform.buildsUnavailable == "StartBuild" {
		return nil, fakeBuildsUnavailable()
	}
	build := f.findBuild(req.GetBuildId())
	if build == nil {
		return nil, connect.NewError(connect.CodeNotFound, "build not found")
	}
	if _, ok := f.platform.uploads[build.GetId()]; !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "source not uploaded")
	}
	build.Status = buildv1.BuildStatus_BUILD_STATUS_QUEUED
	return &buildv1.StartBuildResponse{Build: build}, nil
}

func fakeBuildsUnavailable() *connect.Error {
	reason := errors.New("no cluster in this install accepts builds")
	connectErr := rpcerr.New(connect.CodeFailedPrecondition, reason)
	detail, err := connectproto.NewErrorDetail(&buildv1.BuildsUnavailable{})
	if err != nil {
		panic(err)
	}
	connectErr = connectErr.WithDetail(detail)
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
	ctx context.Context,
	req *buildv1.GetBuildRequest,
) (*buildv1.GetBuildResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: buildv1connect.BuildServiceGetBuildProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	build := f.findBuild(req.GetBuildId())
	if build == nil {
		return nil, connect.NewError(connect.CodeNotFound, "build not found")
	}
	if build.GetStatus() == buildv1.BuildStatus_BUILD_STATUS_QUEUED ||
		build.GetStatus() == buildv1.BuildStatus_BUILD_STATUS_RUNNING {
		f.advance(build)
	}
	return &buildv1.GetBuildResponse{Build: build}, nil
}

func (s *fakeBuildService) ListBuilds(
	ctx context.Context,
	req *buildv1.ListBuildsRequest,
) (*buildv1.ListBuildsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: buildv1connect.BuildServiceListBuildsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var builds []*buildv1.Build
	for _, b := range slices.Backward(f.platform.builds) {
		if b.GetResourceId() == req.GetResourceId() {
			builds = append(builds, b)
		}
	}
	return &buildv1.ListBuildsResponse{Builds: builds}, nil
}

func (s *fakeBuildService) CancelBuild(
	ctx context.Context,
	req *buildv1.CancelBuildRequest,
) (*buildv1.CancelBuildResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: buildv1connect.BuildServiceCancelBuildProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	build := f.findBuild(req.GetBuildId())
	if build == nil {
		return nil, connect.NewError(connect.CodeNotFound, "build not found")
	}
	build.Status = buildv1.BuildStatus_BUILD_STATUS_CANCELED
	build.Message = "canceled by a user"
	build.FinishedAt = timestamppb.Now()
	return &buildv1.CancelBuildResponse{Build: build}, nil
}

type fakeAccessService struct {
	observabilityv1connect.UnimplementedObservabilityAccessServiceHandler

	api *fakeAPI
}

func (s *fakeAccessService) GetObservabilityAccess(
	ctx context.Context,
	_ *observabilityv1.GetObservabilityAccessRequest,
) (*observabilityv1.GetObservabilityAccessResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: observabilityv1connect.ObservabilityAccessServiceGetObservabilityAccessProcedure},
		info.RequestHeader(),
	); err != nil {
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
	return resp, nil
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
	ctx context.Context,
	req *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: observabilityv1connect.ObservabilityProxyServiceQueryLogsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.platform.logQueries = append(f.platform.logQueries, req)
	f.mu.Unlock()
	base := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	lines := fakeLogLines(req.GetLabels(), req.GetResourceIds())
	entries := make([]*observabilityv1.LogEntry, 0, len(lines))
	for i, line := range lines {
		ts := base.Add(time.Duration(i) * time.Second)
		entries = append(entries, &observabilityv1.LogEntry{Timestamp: timestamppb.New(ts), Body: line})
	}
	if req.GetOrder() == observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST {
		slices.Reverse(entries)
	}
	return &observabilityv1.QueryLogsResponse{Entries: entries}, nil
}

func (s *fakeProxyService) TailLogs(
	ctx context.Context,
	_ *observabilityv1.TailLogsRequest,
	stream observabilityv1connect.ObservabilityProxyServiceTailLogsServerStream,
) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	if _, _, err := s.api.authenticate(
		connect.Spec{Procedure: observabilityv1connect.ObservabilityProxyServiceTailLogsProcedure},
		info.RequestHeader(),
	); err != nil {
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
