package resource

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"connectrpc.com/connect"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/logstream"
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
		message = buildStatusLabel(status)
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
	line := trimNewline(body)
	_, err := fmt.Fprintf(w.out, "  │ %s\n", line)
	return err
}

type buildFollower struct {
	clients    platformClients
	host       string
	token      string
	out        *syncWriter
	emit       logstream.Emit
	quietState bool
}

func (f *buildFollower) emitLog(entry *observabilityv1.LogEntry) error {
	if f.emit == nil {
		return f.out.logLine(entry)
	}
	f.out.mu.Lock()
	defer f.out.mu.Unlock()
	return f.emit(entry)
}

func (f *buildFollower) note(format string, args ...any) {
	if f.quietState {
		return
	}
	f.out.printf(format, args...)
}

func (f *buildFollower) getBuild(ctx context.Context, buildID string) (*buildv1.Build, error) {
	req := connect.NewRequest(&buildv1.GetBuildRequest{BuildId: buildID})
	authorization := authHeaderFor(f.token)
	req.Header().Set("Authorization", authorization)
	client := f.clients.Builds(f.host)
	resp, err := client.GetBuild(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get build %s: %w", buildID, err)
	}
	return resp.Msg.GetBuild(), nil
}

func (f *buildFollower) follow(ctx context.Context, build *buildv1.Build, workspaceID string) (*buildv1.Build, error) {
	logCtx, stopLogs := context.WithCancel(ctx)
	defer stopLogs()
	logsDone := make(chan struct{})
	backfilled := make(chan struct{})
	markBackfilled := sync.OnceFunc(func() { close(backfilled) })
	logsStarted := false
	startLogs := func(current *buildv1.Build) {
		if logsStarted || current.GetClusterId() == "" {
			return
		}
		logsStarted = true
		go func() {
			defer close(logsDone)
			defer markBackfilled()
			f.streamLogs(logCtx, current, workspaceID, markBackfilled)
		}()
	}
	waitLogs := func() {
		stopLogs()
		if logsStarted {
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
		description := describeBuild(current)
		f.note("%s\n", description)
	}
	report(build)
	startLogs(build)

	ticker := time.NewTicker(f.clients.PollInterval)
	defer ticker.Stop()
	buildID := build.GetId()
	for !buildFinished(lastStatus) {
		select {
		case <-ctx.Done():
			waitLogs()
			return build, f.detach(build)
		case <-ticker.C:
		}
		next, err := f.getBuild(ctx, buildID)
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

	if logsStarted {
		select {
		case <-ctx.Done():
		case <-backfilled:
		}
		drain := time.NewTimer(f.clients.LogDrain)
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

func (f *buildFollower) detach(build *buildv1.Build) error {
	id := build.GetId()
	f.out.printf("Detached. Build %s keeps running.\n", id)
	f.out.printf("  Follow it: loco builds logs %s -f\n", id)
	f.out.printf("  Cancel it: loco builds cancel %s\n", id)
	return &detachedError{buildID: id}
}

func (f *buildFollower) streamLogs(
	ctx context.Context,
	build *buildv1.Build,
	workspaceID string,
	backfilled func(),
) {
	access := f.clients.Access(f.host)
	clusterID := build.GetClusterId()
	buildID := build.GetId()
	proxies, err := logstream.Proxies(ctx, access, f.token, workspaceID, clusterID)
	if err != nil {
		if ctx.Err() == nil {
			f.note("  Build logs are unavailable: %v\n", err)
		}
		return
	}
	logs := f.clients.logClient(f.token)
	filter := logstream.BuildFilter(workspaceID, buildID)
	start := build.GetCreatedAt().AsTime()
	now := time.Now()
	window := logstream.Window{Start: start, End: now, Follow: true, Backfilled: backfilled}
	streamErr := logs.Stream(ctx, proxies, filter, window, f.emitLog)
	if streamErr != nil && ctx.Err() == nil {
		f.note("  Build logs stopped: %v\n", streamErr)
	}
}

func buildFinished(status buildv1.BuildStatus) bool {
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

func buildStatusLabel(status buildv1.BuildStatus) string {
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

func buildDuration(build *buildv1.Build) string {
	if build.GetStartedAt() == nil || build.GetFinishedAt() == nil {
		return ""
	}
	started := build.GetStartedAt().AsTime()
	finished := build.GetFinishedAt().AsTime()
	elapsed := finished.Sub(started).Round(time.Second)
	return elapsed.String()
}

func describeBuild(build *buildv1.Build) string {
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
		if elapsed := buildDuration(build); elapsed != "" {
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
		label := buildStatusLabel(status)
		return fmt.Sprintf("Build %s is %s", id, label)
	}
}
