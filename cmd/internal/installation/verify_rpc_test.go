package installation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
)

const testProbeResource = "fixture-resource"

type verificationAPI struct {
	cancel context.CancelFunc
	configv1connect.UnimplementedConfigServiceHandler
	planv1connect.UnimplementedPlanServiceHandler
	resourcev1connect.UnimplementedResourceServiceHandler
	secretv1connect.UnimplementedSecretServiceHandler
	buildv1connect.UnimplementedBuildServiceHandler
	t                               *testing.T
	loseProvision                   bool
	name                            string
	provisioned, secretSet, deleted bool
}

func (api *verificationAPI) Plan(_ context.Context, request *planv1.PlanRequest) (*planv1.PlanResponse, error) {
	file, err := locofile.Parse(request.GetFile())
	if err != nil {
		api.t.Fatal(err)
	}
	api.name = file.Partial
	return &planv1.PlanResponse{Revision: 7}, nil
}

func (api *verificationAPI) Apply(_ context.Context, request *planv1.ApplyRequest) (*planv1.ApplyResponse, error) {
	if len(request.GetProvision()) != 1 || request.GetProvision()[0] != api.name || request.GetRevision() != 7 {
		api.t.Errorf("invalid provision: %v", request)
	}
	api.provisioned = true
	if api.loseProvision {
		return nil, connect.NewError(connect.CodeUnavailable, "lost acknowledgement")
	}
	return &planv1.ApplyResponse{}, nil
}

func (api *verificationAPI) GetResource(
	_ context.Context,
	request *resourcev1.GetResourceRequest,
) (*resourcev1.GetResourceResponse, error) {
	if !api.provisioned || request.GetNameKey().GetName() != api.name {
		api.t.Error("lookup before provisioning")
	}
	return &resourcev1.GetResourceResponse{
		Resource: &resourcev1.Resource{Id: testProbeResource, Partial: &api.name},
	}, nil
}

func (api *verificationAPI) SetSecrets(
	_ context.Context,
	request *secretv1.SetSecretsRequest,
) (*secretv1.SetSecretsResponse, error) {
	if request.GetResourceId() != testProbeResource || request.GetValues()[verificationSecretName] == "" {
		api.t.Error("missing probe secret")
	}
	api.secretSet = true
	return &secretv1.SetSecretsResponse{}, nil
}

func (api *verificationAPI) CreateBuild(
	_ context.Context,
	request *buildv1.CreateBuildRequest,
) (*buildv1.CreateBuildResponse, error) {
	api.cancel()
	if !api.secretSet || request.GetSourceSize() == 0 {
		api.t.Error("build before source and secret")
	}
	return nil, connect.NewError(connect.CodeUnavailable, "build fixture failure")
}

func (api *verificationAPI) DeleteResource(
	ctx context.Context,
	request *resourcev1.DeleteResourceRequest,
) (*resourcev1.DeleteResourceResponse, error) {
	if ctx.Err() != nil || request.GetResourceId() != testProbeResource {
		api.t.Error("invalid cleanup")
	}
	api.deleted = true
	return &resourcev1.DeleteResourceResponse{}, nil
}

func TestVerificationCleansProvisionedResourceAfterFailure(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "build-failed", true: "provision-response-lost"}[lost], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			api := &verificationAPI{t: t, loseProvision: lost, cancel: cancel}
			server := connect.NewServer()
			configv1connect.RegisterConfigServiceHandler(server, api)
			planv1connect.RegisterPlanServiceHandler(server, api)
			resourcev1connect.RegisterResourceServiceHandler(server, api)
			secretv1connect.RegisterSecretServiceHandler(server, api)
			buildv1connect.RegisterBuildServiceHandler(server, api)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			httpServer := httptest.NewServer(mux)
			defer httpServer.Close()
			err := verifyApplication(
				ctx,
				rpc.ForHost(httpServer.URL, "fixture-session"),
				Worker{
					Region: "us-east-1", AppsDomain: "apps.example.com",
					MetricsServer: MetricsServer{Enabled: true, KubeletInsecureTLS: false},
				},
				&environmentv1.Environment{Id: "fixture-environment", WorkspaceId: "fixture-workspace"},
				"busybox@sha256:fixture",
			)
			if !errors.Is(err, errVerification) || !api.provisioned || !api.deleted || api.secretSet == lost {
				t.Fatalf("err=%v provisioned=%v secret=%v deleted=%v", err, api.provisioned, api.secretSet, api.deleted)
			}
		})
	}
}

func (*verificationAPI) GetConfig(
	context.Context,
	*configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	return &configv1.GetConfigResponse{
		ServiceDefaults: &configv1.DefaultServiceConfig{Cpu: "100m", Memory: "64Mi", MinReplicas: 1, MaxReplicas: 1},
	}, nil
}
