package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
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
			s := NewBuildServer(nil, nil, tt.bucket, tt.config, limits.Defaults())

			createReq := &buildv1.CreateBuildRequest{
				ResourceId:     resourceID,
				DockerfilePath: testDockerfile,
				SourceSize:     1,
			}
			_, createErr := s.CreateBuild(ctx, createReq)
			if code := connectCode(t, createErr); code != connect.CodeFailedPrecondition {
				t.Fatalf("create code = %v, want failed precondition", code)
			}

			startReq := &buildv1.StartBuildRequest{BuildId: buildID}
			_, startErr := s.StartBuild(ctx, startReq)
			if code := connectCode(t, startErr); code != connect.CodeFailedPrecondition {
				t.Fatalf("start code = %v, want failed precondition", code)
			}
		})
	}
}

func TestBuildToProtoReportsADeletedImage(t *testing.T) {
	deletedAt := time.Now()
	if buildToProto(genDb.Build{ImageDeletedAt: &deletedAt}).GetImageDeletedAt() == nil {
		t.Fatal("a build with a deleted image reports none")
	}
	if buildToProto(genDb.Build{}).GetImageDeletedAt() != nil {
		t.Fatal("a build with its image reports it deleted")
	}
}
