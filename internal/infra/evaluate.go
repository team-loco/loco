package infra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	maxDiagnosticBytes = 128 << 10
)

type Definition struct {
	File        string
	ModuleRoot  string
	ProjectRoot string
}

type Evaluator struct {
	GoBinary       string
	CompileTimeout time.Duration
	RunTimeout     time.Duration
}

type BuildRequest struct {
	Context    string
	Dockerfile string
}

func Discover(start, file, projectRoot string) (*Definition, error) {
	if file == "" {
		dir, err := filepath.Abs(start)
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		for {
			candidate := filepath.Join(dir, ".loco", "main.go")
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				file = candidate
				break
			}
			if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if file == "" {
		return nil, errors.New("no Go infrastructure definition found; run loco init")
	}
	if filepath.Ext(file) != ".go" {
		return nil, errors.New("infrastructure definition must be a Go source file")
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(start, file)
	}
	file, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("resolve definition path: %w", err)
	}
	if _, statErr := os.Stat(file); statErr != nil {
		return nil, fmt.Errorf("read definition: %w", statErr)
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return nil, fmt.Errorf("resolve definition path: %w", err)
	}
	moduleRoot := filepath.Dir(file)
	if _, statErr2 := os.Stat(filepath.Join(moduleRoot, "go.mod")); statErr2 != nil {
		return nil, fmt.Errorf("definition requires its own go.mod: %w", statErr2)
	}
	if projectRoot == "" {
		projectRoot = moduleRoot
		if filepath.Base(moduleRoot) == ".loco" {
			projectRoot = filepath.Dir(moduleRoot)
		}
	} else if !filepath.IsAbs(projectRoot) {
		projectRoot = filepath.Join(start, projectRoot)
	}
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	projectRoot, err = filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	return &Definition{File: file, ModuleRoot: moduleRoot, ProjectRoot: projectRoot}, nil
}

func (e Evaluator) Evaluate(
	ctx context.Context,
	definition *Definition,
	authorContext *loco.Context,
) (*loco.Manifest, error) {
	if definition == nil {
		return nil, errors.New("infrastructure definition is required")
	}
	compiler := e.GoBinary
	if compiler == "" {
		compiler = "go"
	}
	compiler, err := exec.LookPath(compiler)
	if err != nil {
		return nil, errors.New("go is required to evaluate infrastructure; install the supported Go toolchain")
	}
	tempDir, err := os.MkdirTemp("", "loco-infra-")
	if err != nil {
		return nil, fmt.Errorf("create evaluation directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	binary := filepath.Join(tempDir, "definition")
	compileCtx, cancelCompile := context.WithTimeout(ctx, timeout(e.CompileTimeout, 2*time.Minute))
	defer cancelCompile()
	compile := exec.CommandContext(compileCtx, compiler, "build", "-mod=readonly", "-trimpath", "-o", binary, ".")
	compile.Dir = definition.ModuleRoot
	compile.Env = evaluationEnv(true)
	configureProcess(compile)
	diagnostics := &limitedBuffer{limit: maxDiagnosticBytes}
	compile.Stdout = diagnostics
	compile.Stderr = diagnostics
	if runErr := compile.Run(); runErr != nil {
		if compileCtx.Err() != nil {
			return nil, fmt.Errorf("compile infrastructure: %w", compileCtx.Err())
		}
		return nil, fmt.Errorf("compile infrastructure: %w\n%s", runErr, diagnostics.String())
	}
	authorContext.ProjectRoot = definition.ProjectRoot
	input, err := protojson.Marshal(authorContext)
	if err != nil {
		return nil, fmt.Errorf("encode evaluation context: %w", err)
	}
	runCtx, cancelRun := context.WithTimeout(ctx, timeout(e.RunTimeout, 30*time.Second))
	defer cancelRun()
	run := exec.CommandContext(runCtx, binary)
	run.Dir = definition.ProjectRoot
	run.Env = evaluationEnv(false)
	run.Stdin = bytes.NewReader(input)
	configureProcess(run)
	output := &limitedBuffer{limit: maxManifestBytes}
	diagnostics = &limitedBuffer{limit: maxDiagnosticBytes}
	run.Stdout = output
	run.Stderr = diagnostics
	if runErr2 := run.Run(); runErr2 != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("evaluate infrastructure: %w", runCtx.Err())
		}
		return nil, fmt.Errorf("evaluate infrastructure: %w\n%s", runErr2, diagnostics.String())
	}
	var manifest loco.Manifest
	if decodeErr := DecodeJSON(bytes.NewReader(output.Bytes()), &manifest); decodeErr != nil {
		return nil, fmt.Errorf("decode infrastructure manifest: %w", decodeErr)
	}
	if normalizeErr := ValidateManifest(&manifest); normalizeErr != nil {
		return nil, fmt.Errorf("validate infrastructure manifest: %w", normalizeErr)
	}
	return &manifest, nil
}

func ResolveBuild(projectRoot string, source *loco.DockerBuild) (*BuildRequest, error) {
	if source == nil {
		return nil, errors.New("docker build source is required")
	}
	buildContext, err := containedPath(projectRoot, source.Context)
	if err != nil {
		return nil, fmt.Errorf("resolve build context: %w", err)
	}
	dockerfile, err := containedPath(buildContext, source.Dockerfile)
	if err != nil {
		return nil, fmt.Errorf("resolve Dockerfile: %w", err)
	}
	return &BuildRequest{Context: buildContext, Dockerfile: dockerfile}, nil
}

func containedPath(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", errors.New("source paths must be relative")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("source path escapes its root")
	}
	return resolved, nil
}

func evaluationEnv(compile bool) []string {
	keys := []string{"PATH", "HOME", "TMPDIR", "TEMP", "TMP"}
	if compile {
		keys = append(keys, "GOCACHE", "GOMODCACHE", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "CGO_ENABLED")
	}
	env := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "GOWORK=off")
}

func timeout(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("evaluation output exceeds its limit")
	}
	return b.Buffer.Write(p)
}

var _ io.Writer = (*limitedBuffer)(nil)
