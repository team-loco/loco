package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/imageresolver"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
)

const (
	testDigest      = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testPublicImage = "nginx:1.27"
)

type fakeResolver struct {
	digest string
	err    error
	calls  []string
}

func (f *fakeResolver) Resolve(_ context.Context, ref name.Reference) (string, error) {
	refName := ref.Name()
	f.calls = append(f.calls, refName)
	return f.digest, f.err
}

func connectCode(t *testing.T, err error) connect.Code {
	t.Helper()
	public := publicError(err)
	if public == nil {
		t.Fatalf("err = %v, want a connect or coded error", err)
	}
	return public.Code()
}

func TestPinPublicImage(t *testing.T) {
	resourceID := uuid.New()
	buildID := uuid.NewString()

	tests := []struct {
		name      string
		src       *deploymentv1.BuildSource
		resolver  *fakeResolver
		wantCode  connect.Code
		wantImage string
		wantCalls []string
	}{
		{
			name:      "tagged docker hub image",
			src:       &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage},
			resolver:  &fakeResolver{digest: testDigest},
			wantImage: "index.docker.io/library/nginx@" + testDigest,
		},
		{
			name:      "ghcr image",
			src:       &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: "ghcr.io/acme/app:v1"},
			resolver:  &fakeResolver{digest: testDigest},
			wantImage: "ghcr.io/acme/app@" + testDigest,
		},
		{
			name: "tag and digest",
			src: &deploymentv1.BuildSource{
				Type:  buildSourceTypeImage,
				Image: "nginxinc/nginx-unprivileged:1.31.6-alpine@" + testDigest,
			},
			resolver:  &fakeResolver{digest: testDigest},
			wantImage: "index.docker.io/nginxinc/nginx-unprivileged@" + testDigest,
			wantCalls: []string{"index.docker.io/nginxinc/nginx-unprivileged@" + testDigest},
		},
		{
			name:     "build id set",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage, BuildId: &buildID},
			resolver: &fakeResolver{digest: testDigest},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "no image",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeImage},
			resolver: &fakeResolver{digest: testDigest},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "invalid reference",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: "UPPER/Case::bad"},
			resolver: &fakeResolver{digest: testDigest},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name: "loco registry image",
			src: &deploymentv1.BuildSource{
				Type:  buildSourceTypeImage,
				Image: "registry.loco.test/builds/ws-1/app@" + testDigest,
			},
			resolver: &fakeResolver{digest: testDigest},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "unresolvable image",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: "ghcr.io/acme/private:v1"},
			resolver: &fakeResolver{err: errors.New("unauthorized")},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "buildpack",
			src:      &deploymentv1.BuildSource{Type: "buildpack", Image: testPublicImage},
			resolver: &fakeResolver{digest: testDigest},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "docker",
			src:      &deploymentv1.BuildSource{Type: "docker", Image: testPublicImage},
			resolver: &fakeResolver{digest: testDigest},
			wantCode: connect.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pinned, err := pinBuildSource(ctx, nil, tt.resolver, testRegistryHost, resourceID, tt.src)
			if tt.wantCode != 0 {
				if code := connectCode(t, err); code != tt.wantCode {
					t.Fatalf("code = %v, want %v (%v)", code, tt.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pin: %v", err)
			}
			if image := pinned.GetImage(); image != tt.wantImage {
				t.Fatalf("image = %q, want %q", image, tt.wantImage)
			}
			if sourceType := pinned.GetType(); sourceType != buildSourceTypeImage {
				t.Fatalf("type = %q, want image", sourceType)
			}
			if tt.wantCalls != nil && !slices.Equal(tt.resolver.calls, tt.wantCalls) {
				t.Fatalf("resolved %q, want %q", tt.resolver.calls, tt.wantCalls)
			}
		})
	}
}

func TestPinBuildSourceRequiresSource(t *testing.T) {
	ctx := context.Background()
	resolver := &fakeResolver{digest: testDigest}
	resourceID := uuid.New()
	_, err := pinBuildSource(ctx, nil, resolver, "", resourceID, nil)
	if code := connectCode(t, err); code != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want invalid argument", code)
	}
}

func TestImageErrorsOnWire(t *testing.T) {
	const deniedMessage = `Access to image "nginx:1.27" was denied. ` +
		`It may require registry credentials; only public images are supported.`
	tests := []struct {
		name       string
		image      string
		sourceType string
		cause      error
		want       string
	}{
		{
			name:  "not found",
			cause: &transport.Error{StatusCode: http.StatusNotFound},
			want:  `Image "nginx:1.27" was not found. Check the repository and tag or digest.`,
		},
		{
			name:  "manifest unknown",
			cause: &transport.Error{Errors: []transport.Diagnostic{{Code: transport.ManifestUnknownErrorCode}}},
			want:  `Image "nginx:1.27" was not found. Check the repository and tag or digest.`,
		},
		{
			name:  "unauthorized",
			cause: &transport.Error{StatusCode: http.StatusUnauthorized},
			want:  deniedMessage,
		},
		{
			name:  "forbidden",
			cause: &transport.Error{StatusCode: http.StatusForbidden},
			want:  deniedMessage,
		},
		{
			name: "registry denial",
			cause: &transport.Error{
				Errors: []transport.Diagnostic{
					{Code: transport.DeniedErrorCode, Message: "secret registry.internal token=password"},
				},
			},
			want: deniedMessage,
		},
		{
			name:  "invalid",
			image: "https://user:" + t.Name() + "@registry.internal/app",
			want:  "Invalid image reference. Use a repository with a tag or digest.",
		},
		{
			name:       "unsupported source",
			sourceType: "unsupported",
			want:       "Unsupported build type. Use dockerfile or image.",
		},
		{
			name:  "unsupported platform",
			cause: imageresolver.ErrPlatformUnsupported,
			want:  `Image "nginx:1.27" has no linux/amd64 variant.`,
		},
		{
			name:  "registry outage",
			cause: errors.New("registry.internal token=password"),
			want:  `Could not resolve image "nginx:1.27". The registry may be unavailable; try again.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := testPublicImage
			if tt.image != "" {
				image = tt.image
			}
			sourceType := buildSourceTypeImage
			if tt.sourceType != "" {
				sourceType = tt.sourceType
			}
			resolver := &fakeResolver{err: tt.cause}
			workspaceID := uuid.New()
			ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{{
				EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite,
			}}})
			server := &DeploymentServer{resolver: resolver, registryHost: testRegistryHost, authz: authz.New(nil, nil)}
			_, err := server.ResolveImage(
				ctx,
				&deploymentv1.ResolveImageRequest{WorkspaceId: workspaceID.String(), Image: image},
			)
			if sourceType != buildSourceTypeImage {
				_, err = pinBuildSource(
					ctx,
					nil,
					resolver,
					testRegistryHost,
					uuid.New(),
					&deploymentv1.BuildSource{Type: sourceType, Image: image},
				)
			}
			public, ok := errors.AsType[*connect.Error](wireError(err))
			if !ok || public.Code() != connect.CodeInvalidArgument {
				t.Fatalf("wire error = %v", public)
			}
			if public.Message() != tt.want {
				t.Fatalf("message = %q, want %q", public.Message(), tt.want)
			}
			if len(public.Details()) != 1 {
				t.Fatalf("details = %v", public.Details())
			}
			detail, detailErr := connectproto.UnmarshalErrorDetail(public.Details()[0])
			if detailErr != nil {
				t.Fatal(detailErr)
			}
			violations, ok := detail.(*validate.Violations)
			if !ok || len(violations.GetViolations()) != 1 {
				t.Fatalf("detail = %v", detail)
			}
			violation := violations.GetViolations()[0]
			elements := violation.GetField().GetElements()
			if len(elements) != 1 || elements[0].GetFieldName() != "image" || violation.GetMessage() != tt.want {
				t.Fatalf("violation = %v", violation)
			}
			planner := &PlanServer{resolver: resolver, registryHost: testRegistryHost}
			if sourceType != buildSourceTypeImage {
				return
			}
			images := planner.resolveImages(t.Context(), map[string]locofile.Service{"image-service": {Image: image}})
			planErrors := planErrorsToProto([]configplan.Error{{Err: images[image].Err}})
			if got := planErrors[0].GetMessage(); got != tt.want {
				t.Fatalf("plan message = %q", got)
			}
		})
	}
}

func TestResolveImage(t *testing.T) {
	workspaceID := uuid.New()
	resolver := &fakeResolver{digest: testDigest}
	server := &DeploymentServer{resolver: resolver, authz: authz.New(nil, nil)}
	req := &deploymentv1.ResolveImageRequest{WorkspaceId: workspaceID.String(), Image: testPublicImage}
	if _, err := server.ResolveImage(t.Context(), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated resolve: %v", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatal("resolved before authorization")
	}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{{
		EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite,
	}}})
	result, err := server.ResolveImage(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if want := "index.docker.io/library/nginx@" + testDigest; result.GetImage() != want {
		t.Fatalf("image = %q, want %q", result.GetImage(), want)
	}
}
