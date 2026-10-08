package resource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"connectrpc.com/connect"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/internal/sourcepack"
)

const (
	sourceContentType       = "application/gzip"
	uploadErrorBodyMaxBytes = 4096
)

var (
	errUploadRejected = errors.New(
		"the storage service rejected the source upload; the upload URL may have expired " +
			"or the archive no longer matches the size it was signed for. Run the deploy again",
	)
	s3ErrorCode = regexp.MustCompile(`<Code>([^<]+)</Code>`)
)

type sourceBuilder struct {
	follower *buildFollower
}

func (b *sourceBuilder) authorize(req connect.AnyRequest) {
	authorization := authHeaderFor(b.follower.token)
	req.Header().Set("Authorization", authorization)
}

func dockerfileInContext(projectPath, dockerfilePath string) (string, error) {
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	if !filepath.IsAbs(dockerfilePath) {
		cleaned := filepath.Clean(dockerfilePath)
		return filepath.ToSlash(cleaned), nil
	}
	parentPrefix := ".." + string(filepath.Separator)
	rel, err := filepath.Rel(projectPath, dockerfilePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, parentPrefix) {
		return "", fmt.Errorf("the Dockerfile %s is outside the build context %s", dockerfilePath, projectPath)
	}
	return filepath.ToSlash(rel), nil
}

func (b *sourceBuilder) build(
	ctx context.Context,
	resourceID string,
	workspaceID string,
	contextDir string,
	dockerfile string,
) (*buildv1.Build, error) {
	out := b.follower.out
	archive, err := sourcepack.Pack(contextDir, dockerfile)
	if err != nil {
		return nil, fmt.Errorf("pack the build context: %w", err)
	}
	defer func() {
		if removeErr := archive.Remove(); removeErr != nil {
			out.printf("Could not remove %s: %v\n", archive.Path, removeErr)
		}
	}()
	packedSize := formatBytes(archive.Size)
	out.printf("Packed %d files from %s (%s compressed)\n", archive.Files, contextDir, packedSize)

	created, err := b.createBuild(ctx, resourceID, dockerfile, archive)
	if err != nil {
		return nil, err
	}
	buildID := created.GetBuildId()
	out.printf("Uploading the source for build %s\n", buildID)
	uploadURL := created.GetUploadUrl()
	if uploadErr := b.upload(ctx, uploadURL, archive); uploadErr != nil {
		return nil, uploadErr
	}

	startReq := connect.NewRequest(&buildv1.StartBuildRequest{BuildId: buildID})
	b.authorize(startReq)
	client := b.follower.clients.Builds(b.follower.host)
	started, err := client.StartBuild(ctx, startReq)
	if err != nil {
		return nil, fmt.Errorf("start build %s: %w", buildID, err)
	}

	queued := started.Msg.GetBuild()
	build, err := b.follower.follow(ctx, queued, workspaceID)
	if err != nil {
		return nil, err
	}
	if build.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED {
		out.printf("See the build's logs with: loco builds logs %s\n", buildID)
		return nil, &buildFailedError{build: build}
	}
	return build, nil
}

func (b *sourceBuilder) createBuild(
	ctx context.Context,
	resourceID string,
	dockerfile string,
	archive *sourcepack.Archive,
) (*buildv1.CreateBuildResponse, error) {
	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     resourceID,
		DockerfilePath: dockerfile,
		SourceSize:     archive.Size,
	})
	b.authorize(req)
	client := b.follower.clients.Builds(b.follower.host)
	resp, err := client.CreateBuild(ctx, req)
	if connect.CodeOf(err) == connect.CodeInvalidArgument {
		return nil, b.sourceRefused(archive, err)
	}
	if err != nil {
		return nil, fmt.Errorf("create build: %w", err)
	}
	return resp.Msg, nil
}

func (b *sourceBuilder) sourceRefused(archive *sourcepack.Archive, err error) error {
	message := err.Error()
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		message = connectErr.Message()
	}
	largest := make([]string, 0, len(archive.Largest))
	for _, file := range archive.Largest {
		size := formatBytes(file.Size)
		described := fmt.Sprintf("%s (%s)", file.Path, size)
		largest = append(largest, described)
	}
	if len(largest) > 0 {
		listed := strings.Join(largest, ", ")
		b.follower.out.printf("Largest packed files: %s\n", listed)
	}
	b.follower.out.printf("Nothing was uploaded. Leave out files the build does not need with .dockerignore.\n")
	archiveSize := formatBytes(archive.Size)
	return fmt.Errorf("the API refused the %s source archive: %s", archiveSize, message)
}

func (b *sourceBuilder) upload(ctx context.Context, url string, archive *sourcepack.Archive) error {
	f, err := os.Open(archive.Path)
	if err != nil {
		return fmt.Errorf("open the source archive: %w", err)
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return fmt.Errorf("build the upload request: %w", err)
	}
	req.ContentLength = archive.Size
	req.Header.Set("Content-Type", sourceContentType)

	resp, err := b.follower.clients.Upload.Do(req)
	if err != nil {
		if connectionDropped(err) {
			return fmt.Errorf("%w (%w)", errUploadRejected, err)
		}
		return fmt.Errorf("upload the source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	limited := io.LimitReader(resp.Body, uploadErrorBodyMaxBytes)
	body, readErr := io.ReadAll(limited)
	if readErr != nil {
		body = nil
	}
	detail := resp.Status
	if match := s3ErrorCode.FindSubmatch(body); match != nil {
		detail += ", " + string(match[1])
	}
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w (%s)", errUploadRejected, detail)
	}
	return fmt.Errorf("upload the source: the storage service answered %s", detail)
}

func connectionDropped(err error) bool {
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	suffixes := []string{"KiB", "MiB", "GiB", "TiB"}
	suffix := ""
	for _, s := range suffixes {
		value /= unit
		suffix = s
		if value < unit {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}
