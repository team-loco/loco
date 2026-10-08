package sourcepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	dockerfile       = "Dockerfile"
	dockerignore     = ".dockerignore"
	deployDockerfile = "deploy/Dockerfile"
	deployIgnore     = "deploy/Dockerfile.dockerignore"
	mainGo           = "main.go"
	srcMain          = "src/main.go"
	scratchImage     = "FROM scratch\n"
	goSource         = "package main\n"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type entry struct {
	typeflag byte
	body     string
	link     string
}

func readArchive(t *testing.T, data []byte) map[string]entry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	entries := map[string]entry{}
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("tar: %v", nextErr)
		}
		body, readErr := io.ReadAll(tr)
		if readErr != nil {
			t.Fatalf("read %s: %v", hdr.Name, readErr)
		}
		entries[hdr.Name] = entry{typeflag: hdr.Typeflag, body: string(body), link: hdr.Linkname}
	}
	return entries
}

func pack(t *testing.T, dir, dockerfilePath string) (map[string]entry, *Summary) {
	t.Helper()
	var buf bytes.Buffer
	summary, err := Write(&buf, dir, dockerfilePath)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return readArchive(t, buf.Bytes()), summary
}

func names(entries map[string]entry) []string {
	out := make([]string, 0, len(entries))
	for name := range entries {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func TestWriteAlwaysLeavesOutGitAndEnvFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		dockerfile:             scratchImage,
		mainGo:                 goSource,
		".env":                 "SECRET=1\n",
		".env.production":      "SECRET=2\n",
		"web/.env":             "SECRET=3\n",
		"web/.env.local":       "SECRET=4\n",
		"web/app.js":           "app\n",
		".git/config":          "[core]\n",
		"vendor/lib/.git/HEAD": "ref\n",
		dockerignore:           "!.env\n!.git\n",
	})

	entries, summary := pack(t, dir, dockerfile)
	got := names(entries)
	want := []string{dockerignore, dockerfile, mainGo, "vendor/", "vendor/lib/", "web/", "web/app.js"}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if summary.Files != 4 {
		t.Fatalf("files = %d, want 4", summary.Files)
	}
}

func TestWriteHonorsDockerignore(t *testing.T) {
	dir := writeTree(t, map[string]string{
		dockerfile:            scratchImage,
		dockerignore:          "node_modules\n*.log\ndocs\n!docs/keep.md\nDockerfile\n",
		"node_modules/a/b.js": "x\n",
		"debug.log":           "x\n",
		"src/app.log":         "x\n",
		"docs/drop.md":        "x\n",
		"docs/keep.md":        "keep\n",
		srcMain:               goSource,
	})

	entries, summary := pack(t, dir, dockerfile)
	got := names(entries)
	want := []string{dockerignore, dockerfile, "docs/keep.md", "src/", "src/app.log", srcMain}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if summary.IgnoreFile != dockerignore {
		t.Fatalf("ignore file = %q", summary.IgnoreFile)
	}
}

func TestWritePrefersTheDockerfilesOwnIgnoreFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		deployDockerfile: scratchImage,
		deployIgnore:     "assets\n",
		dockerignore:     "src\n",
		"assets/big.bin": "xxxx\n",
		srcMain:          goSource,
	})

	entries, summary := pack(t, dir, deployDockerfile)
	got := names(entries)
	want := []string{
		"deploy/",
		deployDockerfile,
		deployIgnore,
		"src/",
		srcMain,
		dockerignore,
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if summary.IgnoreFile != deployIgnore {
		t.Fatalf("ignore file = %q", summary.IgnoreFile)
	}
}

func TestWriteKeepsADockerfileInsideAnIgnoredDirectory(t *testing.T) {
	dir := writeTree(t, map[string]string{
		deployDockerfile:   scratchImage,
		"deploy/notes.txt": "x\n",
		dockerignore:       "deploy\n",
	})

	entries, _ := pack(t, dir, deployDockerfile)
	got := names(entries)
	want := []string{dockerignore, deployDockerfile}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestWriteKeepsSymlinks(t *testing.T) {
	dir := writeTree(t, map[string]string{
		dockerfile:      scratchImage,
		"config/a.yaml": "a\n",
	})
	if err := os.Symlink("config/a.yaml", filepath.Join(dir, "current.yaml")); err != nil {
		t.Fatal(err)
	}

	entries, _ := pack(t, dir, dockerfile)
	link, ok := entries["current.yaml"]
	if !ok || link.typeflag != tar.TypeSymlink || link.link != "config/a.yaml" {
		t.Fatalf("current.yaml = %+v, want a symlink to config/a.yaml", link)
	}
}

func TestWriteRejectsAMissingDockerfile(t *testing.T) {
	dir := writeTree(t, map[string]string{mainGo: goSource})
	var buf bytes.Buffer
	if _, err := Write(&buf, dir, dockerfile); !errors.Is(err, ErrDockerfileMissing) {
		t.Fatalf("err = %v, want ErrDockerfileMissing", err)
	}
}

func TestWriteRejectsADockerfileOutsideTheContext(t *testing.T) {
	dir := writeTree(t, map[string]string{dockerfile: scratchImage})
	var buf bytes.Buffer
	if _, err := Write(&buf, dir, "../Dockerfile"); err == nil {
		t.Fatal("a Dockerfile outside the context was accepted")
	}
}

func TestPackReportsSizeAndLargestFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		dockerfile:  scratchImage,
		"small.txt": "x",
		"big.bin":   string(bytes.Repeat([]byte("y"), 4096)),
	})

	archive, err := Pack(dir, dockerfile)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := archive.Remove(); removeErr != nil {
			t.Errorf("remove: %v", removeErr)
		}
	})

	info, err := os.Stat(archive.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if archive.Size != info.Size() || archive.Size == 0 {
		t.Fatalf("size = %d, file is %d bytes", archive.Size, info.Size())
	}
	if len(archive.Largest) == 0 || archive.Largest[0].Path != "big.bin" {
		t.Fatalf("largest = %+v, want big.bin first", archive.Largest)
	}
}
