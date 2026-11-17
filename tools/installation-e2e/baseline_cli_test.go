package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBaselineCLIMatchesPublishedReleaseChecksum(t *testing.T) {
	const binary = "published baseline CLI"
	for _, tc := range []struct {
		name, checksum, asset  string
		downloadFails, wantErr bool
	}{
		{name: "verified", checksum: fmt.Sprintf("%x", sha256.Sum256([]byte(binary))), asset: "locoadm-linux-arm64"},
		{name: "modified binary", checksum: strings.Repeat("0", 64), asset: "locoadm-linux-arm64", wantErr: true},
		{
			name: "wrong architecture", checksum: fmt.Sprintf("%x", sha256.Sum256([]byte(binary))),
			asset: "locoadm-linux-amd64", wantErr: true,
		},
		{name: "download failure", downloadFails: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := `#!/bin/sh
set -eu
[ "$1 $2 $3 $4 $5" = 'release download v0.1.2 --repo team-loco/loco' ] || exit 41
[ "$6 $7 $8 $9" = '--pattern locoadm-linux-arm64 --pattern checksums.txt' ] || exit 41
shift 9
[ "$1" = --dir ] && [ "$3" = --clobber ] || exit 42
[ "$DOWNLOAD_FAILS" != true ] || exit 43
printf '%s' "$BASELINE_BINARY" > "$2/locoadm-linux-arm64"
printf '%s  %s\n' "$BASELINE_CHECKSUM" "$BASELINE_ASSET" > "$2/checksums.txt"
`
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("BASELINE_BINARY", binary)
			t.Setenv("BASELINE_CHECKSUM", tc.checksum)
			t.Setenv("BASELINE_ASSET", tc.asset)
			t.Setenv("DOWNLOAD_FAILS", strconv.FormatBool(tc.downloadFails))
			f := fixture{root: dir, dir: dir, platform: "linux/arm64"}
			err := f.downloadBaselineCLI(t.Context(), "v0.1.2")
			if (err != nil) != tc.wantErr {
				t.Fatalf("download = %v, want error %t", err, tc.wantErr)
			}
			path := filepath.Join(dir, "locoadm-baseline")
			if tc.wantErr {
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("unverified CLI installed: %v", err)
				}
				return
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != binary {
				t.Fatalf("verified CLI = %q, %v", data, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("verified CLI is not owner executable: %v, %v", info, err)
			}
		})
	}
}
