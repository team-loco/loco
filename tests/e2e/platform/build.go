package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

// FixtureDockerfile is the Dockerfile of every source-build fixture.
const FixtureDockerfile = "deploy/Dockerfile"

// StartedBuild is a build StartBuild created, uploaded and started.
type StartedBuild struct {
	ID        string
	UploadURL string
	// Status is the status StartBuild answered with.
	Status buildv1.BuildStatus
}

// CopyBuildApp copies the build-app fixture into dir, changed so each label builds a new image.
func (p *Platform) CopyBuildApp(tb testing.TB, dir, label string) {
	tb.Helper()
	p.CopyFixture(tb, "build-app", dir)
	file, err := os.OpenFile(filepath.Join(dir, "main.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		tb.Fatal(err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "\nfunc init() { log.SetPrefix(%q) }\n", label+" "); err != nil {
		tb.Fatal(err)
	}
}

// StartBuild uploads the build-app fixture, changed per label, and starts a build of it for
// resourceID as the application user.
func (p *Platform) StartBuild(tb testing.TB, resourceID, label string) StartedBuild {
	tb.Helper()
	source := tb.TempDir()
	p.CopyBuildApp(tb, source, label)
	archive := p.packSource(tb, source)
	info, err := os.Stat(archive)
	if err != nil {
		tb.Fatal(err)
	}
	created, err := p.User.Build.CreateBuild(tb.Context(), &buildv1.CreateBuildRequest{
		ResourceId: resourceID, DockerfilePath: FixtureDockerfile, SourceSize: info.Size(),
	})
	if err != nil {
		tb.Fatalf("create build %s: %v", label, err)
	}
	upload(tb, created.GetUploadUrl(), archive)
	started, err := p.User.Build.StartBuild(tb.Context(), &buildv1.StartBuildRequest{BuildId: created.GetBuildId()})
	if err != nil {
		tb.Fatalf("start build %s: %v", label, err)
	}
	return StartedBuild{
		ID:        created.GetBuildId(),
		UploadURL: created.GetUploadUrl(),
		Status:    started.GetBuild().GetStatus(),
	}
}
