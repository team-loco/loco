package sourcebuild

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/team-loco/loco/cmd/internal/logstream"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	buildPollInterval = time.Second
	buildLogDrain     = 3 * time.Second
)

type detachedError struct {
	buildID string
}

func (e *detachedError) Error() string {
	return "detached from build " + e.buildID
}

type buildFailedError struct {
	build *buildv1.Build
}

func (e *buildFailedError) Error() string {
	id := e.build.GetId()
	status := e.build.GetStatus()
	message := e.build.GetMessage()
	if message == "" {
		message = StatusLabel(status)
	}
	return fmt.Sprintf("build %s did not succeed: %s", id, message)
}

type syncWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *syncWriter) printf(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.out, format, args...)
}

func (w *syncWriter) logLine(entry *observabilityv1.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	body := entry.GetBody()
	line := logstream.TrimNewline(body)
	_, err := fmt.Fprintf(w.out, "  │ %s\n", line)
	return err
}

type Follower struct {
	clients    *rpc.Clients
	host       string
	token      string
	out        *syncWriter
	Emit       logstream.Emit
	QuietState bool
}

func NewFollower(clients *rpc.Clients, host, token string, out io.Writer) *Follower {
	return &Follower{clients: clients, host: host, token: token, out: &syncWriter{out: out}}
}

func (f *Follower) emitLog(entry *observabilityv1.LogEntry) error {
	if f.Emit == nil {
		return f.out.logLine(entry)
	}
	f.out.mu.Lock()
	defer f.out.mu.Unlock()
	return f.Emit(entry)
}

func (f *Follower) note(format string, args ...any) {
	if f.QuietState {
		return
	}
	f.out.printf(format, args...)
}

func (f *Follower) GetBuild(ctx context.Context, buildID string) (*buildv1.Build, error) {
	req := &buildv1.GetBuildRequest{BuildId: buildID}

	resp, err := f.clients.Build.GetBuild(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get build %s: %w", buildID, err)
	}
	return resp.GetBuild(), nil
}

func (f *Follower) Follow(ctx context.Context, build *buildv1.Build, workspaceID string) (*buildv1.Build, error) {
	logCtx, stopLogs := context.WithCancel(ctx)
	defer stopLogs()
	var logsDone chan struct{}
	startLogs := func(current *buildv1.Build) {
		if logsDone != nil || current.GetClusterId() == "" {
			return
		}
		logsDone = make(chan struct{})
		go func() {
			defer close(logsDone)
			f.streamLogs(logCtx, current, workspaceID)
		}()
	}
	waitLogs := func() {
		stopLogs()
		if logsDone != nil {
			<-logsDone
		}
	}

	lastStatus := buildv1.BuildStatus_BUILD_STATUS_UNSPECIFIED
	report := func(current *buildv1.Build) {
		status := current.GetStatus()
		if status == lastStatus {
			return
		}
		lastStatus = status
		description := Describe(current)
		f.note("%s\n", description)
	}
	report(build)
	startLogs(build)

	ticker := time.NewTicker(buildPollInterval)
	defer ticker.Stop()
	buildID := build.GetId()
	for !Finished(lastStatus) {
		select {
		case <-ctx.Done():
			waitLogs()
			return build, f.detach(build)
		case <-ticker.C:
		}
		next, err := f.GetBuild(ctx, buildID)
		if ctx.Err() != nil {
			waitLogs()
			return build, f.detach(build)
		}
		if err != nil {
			waitLogs()
			return build, err
		}
		build = next
		report(build)
		startLogs(build)
	}

	if logsDone != nil {
		drain := time.NewTimer(buildLogDrain)
		select {
		case <-ctx.Done():
		case <-logsDone:
		case <-drain.C:
		}
		drain.Stop()
	}
	waitLogs()
	return build, nil
}

func (f *Follower) detach(build *buildv1.Build) error {
	id := build.GetId()
	f.out.printf("Detached. Build %s keeps running.\n", id)
	f.out.printf("  Follow it: loco builds logs %s -f\n", id)
	f.out.printf("  Cancel it: loco builds cancel %s\n", id)
	return &detachedError{buildID: id}
}

func (f *Follower) streamLogs(
	ctx context.Context,
	build *buildv1.Build,
	workspaceID string,
) {
	buildID := build.GetId()
	logs := logstream.ForHost(f.host, f.token)
	filter := logstream.BuildFilter(workspaceID, buildID)
	start := build.GetCreatedAt().AsTime()
	now := time.Now()
	window := logstream.Window{Start: start, End: now, Follow: true}
	streamErr := logs.Stream(ctx, filter, window, f.emitLog)
	if streamErr != nil && ctx.Err() == nil {
		f.note("  Build logs stopped: %v\n", streamErr)
	}
}

func Finished(status buildv1.BuildStatus) bool {
	switch status {
	case buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED:
		return true
	case buildv1.BuildStatus_BUILD_STATUS_FAILED:
		return true
	case buildv1.BuildStatus_BUILD_STATUS_CANCELED:
		return true
	default:
		return false
	}
}

func StatusLabel(status buildv1.BuildStatus) string {
	switch status {
	case buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD:
		return "awaiting upload"
	case buildv1.BuildStatus_BUILD_STATUS_QUEUED:
		return "queued"
	case buildv1.BuildStatus_BUILD_STATUS_RUNNING:
		return "running"
	case buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED:
		return "succeeded"
	case buildv1.BuildStatus_BUILD_STATUS_FAILED:
		return "failed"
	case buildv1.BuildStatus_BUILD_STATUS_CANCELED:
		return "canceled"
	default:
		return "unknown"
	}
}

func Duration(build *buildv1.Build) string {
	if build.GetStartedAt() == nil || build.GetFinishedAt() == nil {
		return ""
	}
	started := build.GetStartedAt().AsTime()
	finished := build.GetFinishedAt().AsTime()
	elapsed := finished.Sub(started).Round(time.Second)
	return elapsed.String()
}

func Describe(build *buildv1.Build) string {
	id := build.GetId()
	switch build.GetStatus() {
	case buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD:
		return fmt.Sprintf("Build %s is waiting for its source", id)
	case buildv1.BuildStatus_BUILD_STATUS_QUEUED:
		return fmt.Sprintf("Build %s queued", id)
	case buildv1.BuildStatus_BUILD_STATUS_RUNNING:
		return fmt.Sprintf("Build %s running", id)
	case buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED:
		image := build.GetImageRepository() + "@" + build.GetImageDigest()
		if elapsed := Duration(build); elapsed != "" {
			return fmt.Sprintf("Build %s succeeded in %s: %s", id, elapsed, image)
		}
		return fmt.Sprintf("Build %s succeeded: %s", id, image)
	case buildv1.BuildStatus_BUILD_STATUS_FAILED:
		message := build.GetMessage()
		return fmt.Sprintf("Build %s failed: %s", id, message)
	case buildv1.BuildStatus_BUILD_STATUS_CANCELED:
		message := build.GetMessage()
		return fmt.Sprintf("Build %s canceled: %s", id, message)
	default:
		status := build.GetStatus()
		label := StatusLabel(status)
		return fmt.Sprintf("Build %s is %s", id, label)
	}
}
