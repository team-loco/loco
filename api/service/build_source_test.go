package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
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
				Type:  "image",
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
