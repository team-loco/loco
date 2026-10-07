package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
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

type fakeBuildLookup map[uuid.UUID]genDb.Build

func (f fakeBuildLookup) GetBuildByID(_ context.Context, id uuid.UUID) (genDb.Build, error) {
	b, ok := f[id]
	if !ok {
		return genDb.Build{}, pgx.ErrNoRows
	}
	return b, nil
}

func connectCode(t *testing.T, err error) connect.Code {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("err = %v, want a connect error", err)
	}
	return connectErr.Code()
}

func TestPinDockerfileBuild(t *testing.T) {
	resourceID := uuid.New()
	otherResourceID := uuid.New()
	succeededID := uuid.New()
	runningID := uuid.New()
	foreignID := uuid.New()
	missingID := uuid.New()
	repository := "registry.loco.test/builds/ws-1/" + resourceID.String()
	foreignRepository := "registry.loco.test/builds/ws-2/" + otherResourceID.String()
	digest := testDigest

	builds := fakeBuildLookup{
		succeededID: {
			ID:              succeededID,
			ResourceID:      resourceID,
			Status:          genDb.BuildStatusSucceeded,
			DockerfilePath:  testDockerfile,
			ImageRepository: repository,
			ImageDigest:     &digest,
		},
		runningID: {
			ID:              runningID,
			ResourceID:      resourceID,
			Status:          genDb.BuildStatusRunning,
			ImageRepository: repository,
		},
		foreignID: {
			ID:              foreignID,
			ResourceID:      otherResourceID,
			Status:          genDb.BuildStatusSucceeded,
			ImageRepository: foreignRepository,
			ImageDigest:     &digest,
		},
	}

	succeededIDStr := succeededID.String()
	runningIDStr := runningID.String()
	foreignIDStr := foreignID.String()
	missingIDStr := missingID.String()

	tests := []struct {
		name     string
		src      *deploymentv1.BuildSource
		wantCode connect.Code
	}{
		{
			name: "image set",
			src: &deploymentv1.BuildSource{
				Type:    buildSourceTypeDockerfile,
				Image:   "nginx:1",
				BuildId: &succeededIDStr,
			},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "no build id",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "missing build",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &missingIDStr},
			wantCode: connect.CodeNotFound,
		},
		{
			name:     "build of another resource",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &foreignIDStr},
			wantCode: connect.CodeNotFound,
		},
		{
			name:     "build not finished",
			src:      &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &runningIDStr},
			wantCode: connect.CodeFailedPrecondition,
		},
	}

	resolver := &fakeResolver{digest: testDigest}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := pinBuildSource(ctx, builds, resolver, testRegistryHost, resourceID, tt.src)
			if code := connectCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %v, want %v (%v)", code, tt.wantCode, err)
			}
		})
	}

	t.Run("succeeded build", func(t *testing.T) {
		ctx := context.Background()
		src := &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &succeededIDStr}
		pinned, err := pinBuildSource(ctx, builds, resolver, testRegistryHost, resourceID, src)
		if err != nil {
			t.Fatalf("pin: %v", err)
		}
		want := repository + "@" + testDigest
		if image := pinned.GetImage(); image != want {
			t.Fatalf("image = %q, want %q", image, want)
		}
		if pinnedBuildID := pinned.GetBuildId(); pinnedBuildID != succeededIDStr {
			t.Fatalf("build id = %q, want %q", pinnedBuildID, succeededIDStr)
		}
	})

	if len(resolver.calls) != 0 {
		t.Fatalf("resolver called for dockerfile builds: %v", resolver.calls)
	}
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
			builds := fakeBuildLookup{}
			pinned, err := pinBuildSource(ctx, builds, tt.resolver, testRegistryHost, resourceID, tt.src)
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
		})
	}
}

func TestPinBuildSourceRequiresSource(t *testing.T) {
	ctx := context.Background()
	resolver := &fakeResolver{digest: testDigest}
	builds := fakeBuildLookup{}
	resourceID := uuid.New()
	_, err := pinBuildSource(ctx, builds, resolver, "", resourceID, nil)
	if code := connectCode(t, err); code != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want invalid argument", code)
	}
}
