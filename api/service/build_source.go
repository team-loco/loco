package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

const (
	buildSourceTypeDockerfile = "dockerfile"
	buildSourceTypeImage      = "image"
)

var (
	errCloneDeploymentSpec   = errors.New("failed to clone deployment spec")
	errBuildSourceRequired   = errors.New("spec.service.build is required")
	errDockerfileImageSet    = errors.New("image must not be set for dockerfile deployments; pass build_id instead")
	errDockerfileNoBuildID   = errors.New("build_id is required for dockerfile deployments")
	errBuildNotSucceeded     = errors.New("build has not succeeded")
	errImageBuildIDSet       = errors.New("build_id must not be set for image deployments")
	errImageRequired         = errors.New("image is required for image deployments")
	errImageFromLocoRegistry = errors.New("images from the loco registry can only be deployed through a build")
)

type ImageResolver interface {
	Resolve(ctx context.Context, ref name.Reference) (string, error)
}

type buildLookup interface {
	GetBuildByID(ctx context.Context, id uuid.UUID) (genDb.Build, error)
}

func pinBuildSource(
	ctx context.Context,
	builds buildLookup,
	resolver ImageResolver,
	registryHost string,
	resourceID uuid.UUID,
	src *deploymentv1.BuildSource,
) (*deploymentv1.BuildSource, error) {
	if src == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errBuildSourceRequired)
	}

	sourceType := src.GetType()
	switch sourceType {
	case buildSourceTypeDockerfile:
		return pinDockerfileBuild(ctx, builds, resourceID, src)
	case buildSourceTypeImage:
		return pinPublicImage(ctx, resolver, registryHost, src)
	default:
		unsupported := fmt.Errorf("unsupported build type %q: use \"dockerfile\" or \"image\"", sourceType)
		return nil, connect.NewError(connect.CodeInvalidArgument, unsupported)
	}
}

func pinDockerfileBuild(
	ctx context.Context,
	builds buildLookup,
	resourceID uuid.UUID,
	src *deploymentv1.BuildSource,
) (*deploymentv1.BuildSource, error) {
	if src.GetImage() != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errDockerfileImageSet)
	}
	rawBuildID := src.GetBuildId()
	if rawBuildID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errDockerfileNoBuildID)
	}
	buildID, err := uuid.Parse(rawBuildID)
	if err != nil {
		invalid := fmt.Errorf("invalid build_id: %w", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, invalid)
	}

	build, err := builds.GetBuildByID(ctx, buildID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, ErrBuildNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get build", "error", err, "buildId", buildID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if build.ResourceID != resourceID {
		slog.WarnContext(ctx, "build belongs to another resource", "buildId", buildID, "resourceId", resourceID)
		return nil, connect.NewError(connect.CodeNotFound, ErrBuildNotFound)
	}
	if build.Status != genDb.BuildStatusSucceeded || build.ImageDigest == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errBuildNotSucceeded)
	}

	image := build.ImageRepository + "@" + *build.ImageDigest
	buildIDStr := build.ID.String()
	return &deploymentv1.BuildSource{
		Type:    buildSourceTypeDockerfile,
		Image:   image,
		BuildId: &buildIDStr,
	}, nil
}

func pinPublicImage(
	ctx context.Context,
	resolver ImageResolver,
	registryHost string,
	src *deploymentv1.BuildSource,
) (*deploymentv1.BuildSource, error) {
	if src.GetBuildId() != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errImageBuildIDSet)
	}
	image := src.GetImage()
	if image == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errImageRequired)
	}

	ref, err := name.ParseReference(image)
	if err != nil {
		invalid := fmt.Errorf("invalid image reference %q: %w", image, err)
		return nil, connect.NewError(connect.CodeInvalidArgument, invalid)
	}

	repository := ref.Context()
	imageHost := repository.RegistryStr()
	if registryHost != "" && strings.EqualFold(imageHost, registryHost) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errImageFromLocoRegistry)
	}

	digest, err := resolver.Resolve(ctx, ref)
	if err != nil {
		slog.WarnContext(ctx, "failed to resolve image", "image", image, "error", err)
		unresolved := fmt.Errorf("could not resolve public image %q: %w", image, err)
		return nil, connect.NewError(connect.CodeInvalidArgument, unresolved)
	}

	pinned := repository.Name() + "@" + digest
	return &deploymentv1.BuildSource{
		Type:  buildSourceTypeImage,
		Image: pinned,
	}, nil
}
