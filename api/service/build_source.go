package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	buildSourceTypeDockerfile = "dockerfile"
	buildSourceTypeImage      = "image"
)

var (
	errBuildSourceRequired = rpcerr.NewCoded(connect.CodeInvalidArgument, "spec.service.build is required")
	errBuildImageMismatch  = rpcerr.NewCoded(connect.CodeInvalidArgument, "image does not match the referenced build")
	errDockerfileNoBuildID = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"build_id is required for dockerfile deployments",
	)
	errBuildNotSucceeded = rpcerr.NewCoded(connect.CodeFailedPrecondition, "build has not succeeded")
	errImageBuildIDSet   = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"build_id must not be set for image deployments",
	)
	errImageRequired     = rpcerr.NewCoded(connect.CodeInvalidArgument, "image is required for image deployments")
	errBuildImageDeleted = rpcerr.NewCoded(connect.CodeFailedPrecondition,
		"the build's image was deleted from the registry by image retention; build the source again",
	)
	errImageFromLocoRegistry = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"images from the loco registry can only be deployed through a build",
	)
)

type ImageResolver interface {
	Resolve(ctx context.Context, ref name.Reference) (string, error)
}

func pinBuildSource(
	ctx context.Context,
	builds genDb.Querier,
	resolver ImageResolver,
	registryHost string,
	resourceID uuid.UUID,
	src *deploymentv1.BuildSource,
) (*deploymentv1.BuildSource, error) {
	if src == nil {
		return nil, errBuildSourceRequired
	}

	sourceType := src.GetType()
	switch sourceType {
	case buildSourceTypeDockerfile:
		return pinDockerfileBuild(ctx, builds, resourceID, src)
	case buildSourceTypeImage:
		return pinPublicImage(ctx, resolver, registryHost, src)
	default:
		unsupported := fmt.Errorf("unsupported build type %q: use \"dockerfile\" or \"image\"", sourceType)
		return nil, &invalidInputError{public: publicInvalidRequest, err: unsupported}
	}
}

func pinDockerfileBuild(
	ctx context.Context,
	builds genDb.Querier,
	resourceID uuid.UUID,
	src *deploymentv1.BuildSource,
) (*deploymentv1.BuildSource, error) {
	rawBuildID := src.GetBuildId()
	if rawBuildID == "" {
		return nil, errDockerfileNoBuildID
	}
	buildID, err := uuid.Parse(rawBuildID)
	if err != nil {
		invalid := fmt.Errorf("invalid build_id: %w", err)
		return nil, &invalidInputError{public: publicInvalidRequest, err: invalid}
	}

	build, err := builds.GetBuildByID(ctx, buildID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBuildNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get build %s: %w", buildID, err)
	}
	if build.ResourceID != resourceID {
		slog.WarnContext(ctx, "build belongs to another resource", "buildId", buildID, "resourceId", resourceID)
		return nil, ErrBuildNotFound
	}
	if build.Status != genDb.BuildStatusSucceeded || build.ImageDigest == nil {
		return nil, errBuildNotSucceeded
	}
	if build.ImageDeletedAt != nil {
		return nil, errBuildImageDeleted
	}

	image := build.ImageRepository + "@" + *build.ImageDigest
	if src.GetImage() != "" && src.GetImage() != image {
		return nil, errBuildImageMismatch
	}
	buildIDStr := build.ID.String()
	return &deploymentv1.BuildSource{
		Type:    buildSourceTypeDockerfile,
		Image:   image,
		BuildId: &buildIDStr,
	}, nil
}

func lockPinnedBuildImage(ctx context.Context, qtx *genDb.Queries, pinned *deploymentv1.BuildSource) error {
	rawBuildID := pinned.GetBuildId()
	if rawBuildID == "" {
		return nil
	}
	buildID, err := uuid.Parse(rawBuildID)
	if err != nil {
		return fmt.Errorf("parse pinned build id: %w", err)
	}
	deletedAt, err := qtx.LockBuildImageForDeploy(ctx, buildID)
	if err != nil {
		return fmt.Errorf("lock build %s: %w", buildID, err)
	}
	if deletedAt != nil {
		return errBuildImageDeleted
	}
	return nil
}

func pinPublicImage(
	ctx context.Context,
	resolver ImageResolver,
	registryHost string,
	src *deploymentv1.BuildSource,
) (*deploymentv1.BuildSource, error) {
	if src.GetBuildId() != "" {
		return nil, errImageBuildIDSet
	}
	image := src.GetImage()
	if image == "" {
		return nil, errImageRequired
	}

	ref, err := name.ParseReference(image)
	if err != nil {
		invalid := fmt.Errorf("invalid image reference %q: %w", image, err)
		return nil, &invalidInputError{public: publicInvalidRequest, err: invalid}
	}

	repository := ref.Context()
	imageHost := repository.RegistryStr()
	if registryHost != "" && strings.EqualFold(imageHost, registryHost) {
		return nil, errImageFromLocoRegistry
	}

	digest, err := resolver.Resolve(ctx, ref)
	if err != nil {
		slog.WarnContext(ctx, "failed to resolve image", "image", image, "error", err)
		unresolved := fmt.Errorf("could not resolve public image %q: %w", image, err)
		return nil, &invalidInputError{public: publicInvalidRequest, err: unresolved}
	}

	pinned := repository.Name() + "@" + digest
	return &deploymentv1.BuildSource{
		Type:  buildSourceTypeImage,
		Image: pinned,
	}, nil
}
