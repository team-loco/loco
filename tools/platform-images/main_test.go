package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	const (
		image = "ghcr.io/example/platform"
		amd64 = `{"digest":"sha256:amd64","platform":{"os":"linux","architecture":"amd64"}}`
		arm64 = `{"digest":"sha256:arm64","platform":{"os":"linux","architecture":"arm64","variant":"v8"}}`
		other = `{"digest":"sha256:attestation","platform":{"os":"unknown","architecture":"unknown"}}`
	)
	index := fmt.Sprintf(`{"manifests":[%s,%s,%s]}`, amd64, arm64, other)
	for _, arch := range []string{archAMD64, archARM64} {
		got, err := resolve(image+"@sha256:index", "linux", arch, []byte(index))
		require.NoError(t, err)
		require.Equal(t, image+"@sha256:"+arch, got)
	}
	for _, manifests := range []string{amd64 + "," + other, arm64 + "," + arm64} {
		index := fmt.Sprintf(`{"manifests":[%s]}`, manifests)
		_, err := resolve(image, "linux", archARM64, []byte(index))
		require.ErrorContains(t, err, "exactly one linux/arm64")
	}
}

func TestVerifyBinary(t *testing.T) {
	static := elfBinary(elf.EM_AARCH64, elf.PT_LOAD)
	for _, tc := range []struct {
		name string
		arch string
		data []byte
		want string
	}{
		{name: "amd64", arch: archAMD64, data: elfBinary(elf.EM_X86_64, elf.PT_LOAD)},
		{name: "arm64", arch: archARM64, data: static},
		{name: "wrong machine", arch: archARM64, data: elfBinary(elf.EM_X86_64, elf.PT_LOAD), want: "does not match arm64"},
		{name: "dynamic", arch: archARM64, data: elfBinary(elf.EM_AARCH64, elf.PT_INTERP), want: "statically linked"},
		{name: "invalid", arch: archARM64, data: []byte("invalid ELF"), want: "read ELF entrypoint"},
		{name: "truncated", arch: archARM64, data: static[:80], want: "read ELF entrypoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "entrypoint")
			require.NoError(t, os.WriteFile(path, tc.data, 0o600))
			err := verifyBinary(path, tc.arch)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestVerifyImage(t *testing.T) {
	machines := map[string]elf.Machine{archAMD64: elf.EM_X86_64, archARM64: elf.EM_AARCH64}
	machine, ok := machines[runtime.GOARCH]
	if !ok || runtime.GOOS == "windows" {
		t.Skip("requires a Unix shell on amd64 or arm64")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_ARCH", runtime.GOARCH)
	t.Setenv("TEST_ELF", filepath.Join(dir, "entrypoint"))
	t.Setenv("TEST_REMOVED", filepath.Join(dir, "removed"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(`#!/bin/sh
set -eu
case "$*" in
  "image inspect -- test-image")
    printf '[{"Os":"linux","Architecture":"%s","Config":{"Entrypoint":["/app","serve"]}}]\n' "$TEST_ARCH"
    ;;
  "create -- test-image") echo test-container ;;
  "cp -- test-container:/app "*) cp "$TEST_ELF" "$4" ;;
  "rm -- test-container") touch "$TEST_REMOVED" ;;
  *) exit 1 ;;
esac
`), 0o700))

	entrypoint := filepath.Join(dir, "entrypoint")
	require.NoError(t, os.WriteFile(entrypoint, elfBinary(machine, elf.PT_LOAD), 0o600))
	require.NoError(t, verifyImage(t.Context(), "test-image", "linux", runtime.GOARCH))
	require.FileExists(t, filepath.Join(dir, "removed"))

	require.NoError(t, os.Remove(filepath.Join(dir, "removed")))
	require.NoError(t, os.WriteFile(entrypoint, elfBinary(machine, elf.PT_INTERP), 0o600))
	require.ErrorContains(t, verifyImage(t.Context(), "test-image", "linux", runtime.GOARCH), "statically linked")
	require.FileExists(t, filepath.Join(dir, "removed"))
}

func elfBinary(machine elf.Machine, segment elf.ProgType) []byte {
	data := make([]byte, 128)
	copy(data, "\x7fELF\x02\x01\x01")
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(data[18:], uint16(machine))
	binary.LittleEndian.PutUint32(data[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint64(data[32:], 64)
	binary.LittleEndian.PutUint16(data[52:], 64)
	binary.LittleEndian.PutUint16(data[54:], 56)
	binary.LittleEndian.PutUint16(data[56:], 1)
	binary.LittleEndian.PutUint32(data[64:], uint32(segment))
	return data
}
