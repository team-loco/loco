package infra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	loco "github.com/team-loco/loco/sdk/go"
)

const testProduction = "production"
const testServiceKey = "api"

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func definitionFixture(t *testing.T, main string) *Definition {
	t.Helper()
	project := t.TempDir()
	sdk, err := filepath.Abs("../../sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	module := "module definition-test\n\ngo 1.24.0\n\n" +
		"require github.com/team-loco/loco/sdk/go v0.0.0\n" +
		"replace github.com/team-loco/loco/sdk/go => " + strconv.Quote(sdk) + "\n"
	writeFile(t, filepath.Join(project, ".loco", "go.mod"), module)
	writeFile(t, filepath.Join(project, ".loco", "main.go"), main)
	definition, err := Discover(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestEvaluateCompilesPackageAndSuppliesContext(t *testing.T) {
	definition := definitionFixture(t, `package main
import loco "github.com/team-loco/loco/sdk/go"
func main() { loco.Run(define) }
`)
	writeFile(t, filepath.Join(definition.ModuleRoot, "helper.go"), `package main
import loco "github.com/team-loco/loco/sdk/go"
func define(ctx loco.Context) loco.Stack {
    return loco.Stack{Name: ctx.Environment}
}
`)
	manifest, err := (Evaluator{}).Evaluate(context.Background(), definition, loco.Context{Environment: testProduction})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Stack.Name != testProduction {
		t.Fatalf("context was not supplied: %+v", manifest)
	}
	nested := filepath.Join(definition.ProjectRoot, "app", "src")
	if mkdirAllErr := os.MkdirAll(nested, 0o755); mkdirAllErr != nil {
		t.Fatal(mkdirAllErr)
	}
	found, err := Discover(nested, "", "")
	if err != nil || found.ProjectRoot != definition.ProjectRoot {
		t.Fatalf("nested discovery: %+v, %v", found, err)
	}
}

func TestEvaluateRejectsNoisyOutput(t *testing.T) {
	definition := definitionFixture(t, `package main
import "fmt"
func main() { fmt.Println("log output"); fmt.Println("{}") }
`)
	if _, err := (Evaluator{}).Evaluate(
		context.Background(),
		definition,
		loco.Context{Environment: testProduction},
	); err == nil {
		t.Fatal("accepted noisy manifest output")
	}
}

func TestEvaluateTimeout(t *testing.T) {
	definition := definitionFixture(t, `package main
import "time"
func main() { time.Sleep(time.Hour) }
`)
	_, err := (Evaluator{RunTimeout: 50 * time.Millisecond}).Evaluate(
		context.Background(), definition, loco.Context{Environment: testProduction},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected evaluation timeout, got %v", err)
	}
}

func TestResolveBuildRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, testServiceKey, "Dockerfile"), "FROM scratch\n")
	build, err := ResolveBuild(root, &loco.DockerBuild{Context: testServiceKey, Dockerfile: "Dockerfile"})
	if err != nil || !strings.HasSuffix(build.Dockerfile, filepath.Join(testServiceKey, "Dockerfile")) {
		t.Fatalf("resolve build: %+v, %v", build, err)
	}
	for _, source := range []*loco.DockerBuild{
		{Context: "..", Dockerfile: "Dockerfile"},
		{Context: testServiceKey, Dockerfile: "../api/Dockerfile/../../.."},
	} {
		if _, resolveBuildErr := ResolveBuild(root, source); resolveBuildErr == nil {
			t.Fatal("accepted escaping source path")
		}
	}
}

func TestEvaluationEnvExcludesCredentials(t *testing.T) {
	t.Setenv("LOCO_TOKEN", "do-not-pass")
	t.Setenv("GITHUB_TOKEN", "do-not-pass")
	for _, env := range evaluationEnv(false) {
		if strings.Contains(env, "do-not-pass") {
			t.Fatal("credentials passed to definition")
		}
	}
}
