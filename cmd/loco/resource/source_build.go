package resource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	errBuildsUnavailable = errors.New("this Loco install cannot build from source")
	s3ErrorCode          = regexp.MustCompile(`<Code>([^<]+)</Code>`)
)

// SourceBuilder builds an uploaded source archive on Loco and follows the build to its end.
type SourceBuilder struct {
	follower *buildFollower
}

// NewSourceBuilder returns a builder that talks to the API at host as the token's user and
// prints the build's progress and logs to out.
func NewSourceBuilder(host, token string, out io.Writer) *SourceBuilder {
	return &SourceBuilder{follower: &buildFollower{
		clients: defaultPlatformClients(),
		host:    host,
		token:   token,
		out:     &syncWriter{out: out},
	}}
}

// BuildInput names what one build of a service compiles: the service's resource, the archive
// to upload, and the Dockerfile relative to the context directory of the archive.
type BuildInput struct {
	ResourceID  string
	WorkspaceID string
	Archive     *sourcepack.Archive
	Dockerfile  string
	Context     string
}

func (b *SourceBuilder) authorize(req connect.AnyRequest) {
	authorization := authHeaderFor(b.follower.token)
	req.Header().Set("Authorization", authorization)
}

// PackedArchive prints what an archive holds, as the deploy reports it before the first build.
func PackedArchive(out io.Writer, archive *sourcepack.Archive, dir string) {
	packedSize := formatBytes(archive.Size)
	fmt.Fprintf(out, "Packed %d files from %s (%s compressed)\n", archive.Files, dir, packedSize)
}

// Build creates a build from the input, uploads its archive, starts the build and follows it.
// It returns the build once it succeeded, and an error naming the build when it did not.
func (b *SourceBuilder) Build(ctx context.Context, in BuildInput) (*buildv1.Build, error) {
	out := b.follower.out
	created, err := b.createBuild(ctx, in)
	if err != nil {
		return nil, err
	}
	buildID := created.GetBuildId()
	out.printf("Uploading the source for build %s\n", buildID)
	uploadURL := created.GetUploadUrl()
	if uploadErr := b.upload(ctx, uploadURL, in.Archive); uploadErr != nil {
		return nil, uploadErr
	}

	startReq := connect.NewRequest(&buildv1.StartBuildRequest{BuildId: buildID})
	b.authorize(startReq)
	client := b.follower.clients.Builds(b.follower.host)
	started, err := client.StartBuild(ctx, startReq)
	if unavailable := buildsUnavailable(err); unavailable != nil {
		return nil, unavailable
	}
	if err != nil {
		return nil, fmt.Errorf("start build %s: %w", buildID, err)
	}

	queued := started.Msg.GetBuild()
	build, err := b.follower.follow(ctx, queued, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if build.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED {
		out.printf("See the build's logs with: loco builds logs %s\n", buildID)
		return nil, &buildFailedError{build: build}
	}
	return build, nil
}

func (b *SourceBuilder) createBuild(ctx context.Context, in BuildInput) (*buildv1.CreateBuildResponse, error) {
	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     in.ResourceID,
		DockerfilePath: in.Dockerfile,
		SourceSize:     in.Archive.Size,
		Context:        in.Context,
	})
	b.authorize(req)
	client := b.follower.clients.Builds(b.follower.host)
	resp, err := client.CreateBuild(ctx, req)
	if unavailable := buildsUnavailable(err); unavailable != nil {
		return nil, unavailable
	}
	if connect.CodeOf(err) == connect.CodeInvalidArgument {
		return nil, b.sourceRefused(in.Archive, err)
	}
	if err != nil {
		return nil, fmt.Errorf("create build: %w", err)
	}
	return resp.Msg, nil
}

func buildsUnavailable(err error) error {
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok || connectErr.Code() != connect.CodeFailedPrecondition {
		return nil
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if _, isUnavailable := value.(*buildv1.BuildsUnavailable); isUnavailable {
			reason := connectErr.Message()
			return fmt.Errorf(
				"%w: %s. Set image on the service in loco.yaml to deploy a prebuilt public image instead",
				errBuildsUnavailable,
				reason,
			)
		}
	}
	return nil
}

func (b *SourceBuilder) sourceRefused(archive *sourcepack.Archive, err error) error {
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

func (b *SourceBuilder) upload(ctx context.Context, url string, archive *sourcepack.Archive) error {
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
