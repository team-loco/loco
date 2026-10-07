package ocilayout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

const notRegular = "not a regular file"

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
