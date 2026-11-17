package ocilayout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/team-loco/loco/internal/workloadplatform"
)

const (
	notRegular        = "not a regular file"
	testOS            = "linux"
	amd64Architecture = "amd64"
	arm64Architecture = "arm64"
)

func writeLayout(t *testing.T) (string, int64) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "image")
	path, err := layout.Write(dir, empty.Index)
	if err != nil {
		t.Fatalf("layout.Write: %v", err)
	}
	img, err := random.Image(512, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	if appendErr := path.AppendImage(img); appendErr != nil {
		t.Fatalf("AppendImage: %v", appendErr)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "blobs", "sha256"))
	if err != nil {
		t.Fatalf("read blobs: %v", err)
	}
	var total int64
	for _, e := range entries {
		info, infoErr := e.Info()
		if infoErr != nil {
			t.Fatalf("stat blob: %v", infoErr)
		}
		total += info.Size()
	}
	return dir, total
}

func firstBlob(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "blobs", "sha256"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("read blobs: %v", err)
	}
	return filepath.Join(dir, "blobs", "sha256", entries[0].Name())
}

func TestValidateAcceptsLayout(t *testing.T) {
	dir, size := writeLayout(t)
	got, err := Validate(dir, size)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got != size {
		t.Errorf("size = %d, want %d", got, size)
	}
}

func TestValidateEnforcesSizeCap(t *testing.T) {
	dir, size := writeLayout(t)
	_, err := Validate(dir, size-1)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Validate = %v, want ErrTooLarge", err)
	}
}

func TestValidateRejectsTampering(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, dir string)
		want   string
	}{
		{
			name: "digest mismatch",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(firstBlob(t, dir), []byte("swapped"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "has digest",
		},
		{
			name: "symlinked blob",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				blob := firstBlob(t, dir)
				if err := os.Remove(blob); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/passwd", blob); err != nil {
					t.Fatal(err)
				}
			},
			want: notRegular,
		},
		{
			name: "symlinked index",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				index := filepath.Join(dir, "index.json")
				if err := os.Remove(index); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/hosts", index); err != nil {
					t.Fatal(err)
				}
			},
			want: notRegular,
		},
		{
			name: "symlinked directory",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Symlink("/", filepath.Join(dir, "root")); err != nil {
					t.Fatal(err)
				}
			},
			want: notRegular,
		},
		{
			name: "badly named blob",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "blobs", "sha256", "x"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "not named after",
		},
		{
			name: "other algorithm",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha512"), 0o750); err != nil {
					t.Fatal(err)
				}
			},
			want: "unsupported blob algorithm",
		},
		{
			name: "fifo",
			tamper: func(t *testing.T, dir string) {
				t.Helper()
				pipe := filepath.Join(dir, "pipe")
				if err := syscall.Mkfifo(pipe, 0o600); err != nil {
					t.Skipf("mkfifo: %v", err)
				}
			},
			want: notRegular,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := writeLayout(t)
			tc.tamper(t, dir)
			_, err := Validate(dir, 0)
			if err == nil {
				t.Fatalf("Validate accepted a layout with a %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestBuiltImagePlatformMustMatchTarget(t *testing.T) {
	for _, arch := range []string{amd64Architecture, arm64Architecture} {
		dir := t.TempDir()
		output, err := layout.Write(dir, empty.Index)
		if err != nil {
			t.Fatal(err)
		}
		config := &v1.ConfigFile{OS: testOS, Architecture: arch}
		image, err := mutate.ConfigFile(empty.Image, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := output.AppendImage(image); err != nil {
			t.Fatal(err)
		}
		for _, target := range []workloadplatform.Platform{workloadplatform.AMD64, workloadplatform.ARM64} {
			err := ValidatePlatform(dir, target)
			if (err == nil) != (arch == target.Architecture()) {
				t.Fatalf("%s output for %s: %v", arch, target, err)
			}
		}
	}
}

func TestBuiltImageDescriptorAndConfigMustSupportTarget(t *testing.T) {
	for _, tc := range []struct {
		descriptor, config string
		extraWrongVariant  bool
		valid              bool
	}{
		{arm64Architecture, arm64Architecture, false, true},
		{amd64Architecture, arm64Architecture, false, false},
		{arm64Architecture, amd64Architecture, false, false},
		{arm64Architecture, arm64Architecture, true, false},
	} {
		dir := t.TempDir()
		output, err := layout.Write(dir, empty.Index)
		if err != nil {
			t.Fatal(err)
		}
		image, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: testOS, Architecture: tc.config})
		if err != nil {
			t.Fatal(err)
		}
		if err = output.AppendImage(
			image,
			layout.WithPlatform(v1.Platform{OS: testOS, Architecture: tc.descriptor}),
		); err != nil {
			t.Fatal(err)
		}
		if tc.extraWrongVariant {
			wrong, wrongErr := mutate.ConfigFile(
				empty.Image,
				&v1.ConfigFile{OS: testOS, Architecture: amd64Architecture},
			)
			if wrongErr != nil {
				t.Fatal(wrongErr)
			}
			if err = output.AppendImage(
				wrong,
				layout.WithPlatform(v1.Platform{OS: testOS, Architecture: arm64Architecture}),
			); err != nil {
				t.Fatal(err)
			}
		}
		err = ValidatePlatform(dir, workloadplatform.ARM64)
		if (err == nil) != tc.valid {
			t.Fatalf("descriptor=%s config=%s extra-wrong=%v: %v", tc.descriptor, tc.config, tc.extraWrongVariant, err)
		}
	}
}
