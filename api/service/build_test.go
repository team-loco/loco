package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

func TestBuildServiceDisabledWithoutConfig(t *testing.T) {
	ctx := context.Background()
	resourceID := uuid.NewString()
	buildID := uuid.NewString()
	bucket := newFakeBucket()

	tests := []struct {
		name   string
		config BuildConfig
		bucket SourceBucket
	}{
		{name: "no bucket", config: BuildConfig{RegistryHost: testRegistryHost, SourceMaxBytes: 10}},
		{name: "no registry", config: BuildConfig{SourceMaxBytes: 10}, bucket: bucket},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewBuildServer(nil, nil, tt.bucket, tt.config)

			createReq := connect.NewRequest(&buildv1.CreateBuildRequest{
				ResourceId:     resourceID,
				DockerfilePath: testDockerfile,
				SourceSize:     1,
			})
			_, createErr := s.CreateBuild(ctx, createReq)
			if code := connectCode(t, createErr); code != connect.CodeFailedPrecondition {
				t.Fatalf("create code = %v, want failed precondition", code)
			}

			startReq := connect.NewRequest(&buildv1.StartBuildRequest{BuildId: buildID})
			_, startErr := s.StartBuild(ctx, startReq)
			if code := connectCode(t, startErr); code != connect.CodeFailedPrecondition {
				t.Fatalf("start code = %v, want failed precondition", code)
			}
		})
	}
}

func TestImageRepository(t *testing.T) {
	workspaceID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	resourceID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	suffix := "/ws-" + workspaceID.String() + "/" + resourceID.String()

	tests := []struct {
		host   string
		prefix string
		want   string
	}{
		{host: testRegistryHost, prefix: "builds", want: testRegistryHost + "/builds" + suffix},
		{host: testRegistryHost + "/", prefix: "/team/builds/", want: testRegistryHost + "/team/builds" + suffix},
		{host: testRegistryHost, want: testRegistryHost + suffix},
	}

	for _, tt := range tests {
		if got := imageRepository(tt.host, tt.prefix, workspaceID, resourceID); got != tt.want {
			t.Errorf("imageRepository(%q, %q) = %q, want %q", tt.host, tt.prefix, got, tt.want)
		}
	}
}
