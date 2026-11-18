package main

import (
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	platformOS = "linux"
	archAMD64  = "amd64"
	archARM64  = "arm64"
)

var machineByArch = map[string]elf.Machine{archAMD64: elf.EM_X86_64, archARM64: elf.EM_AARCH64}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	const usage = "usage: platform-images {resolve IMAGE PLATFORM INDEX | image IMAGE PLATFORM | binary FILE PLATFORM}"
	if len(args) < 3 {
		return errors.New(usage)
	}
	operation, target, platform := args[0], args[1], args[2]
	arch, linux := strings.CutPrefix(platform, platformOS+"/")
	if !linux || machineByArch[arch] == elf.EM_NONE {
		return fmt.Errorf("unsupported platform %q", platform)
	}

	switch {
	case operation == "resolve" && len(args) == 4:
		data, err := os.ReadFile(args[3]) //nolint:gosec // The CLI reads the index path supplied by the caller.
		if err != nil {
			return err
		}
		image, err := resolve(target, arch, data)
		if err != nil {
			return err
		}
		fmt.Println(image)
	case operation == "image" && len(args) == 3:
		if err := verifyImage(ctx, target, arch); err != nil {
			return err
		}
		fmt.Printf("%s image and static ELF entrypoint verified\n", platform)
	case operation == "binary" && len(args) == 3:
		if err := verifyBinary(target, arch); err != nil {
			return err
		}
		fmt.Printf("%s static ELF binary verified\n", platform)
	default:
		return errors.New(usage)
	}
	return nil
}

func resolve(image, arch string, data []byte) (string, error) {
	var index struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return "", fmt.Errorf("decode image index: %w", err)
	}
	var count int
	var digest string
	for _, manifest := range index.Manifests {
		if manifest.Platform.OS == platformOS && manifest.Platform.Architecture == arch {
			count++
			digest = manifest.Digest
		}
	}
	if count != 1 || digest == "" {
		return "", fmt.Errorf("image index must contain exactly one %s/%s image with a digest", platformOS, arch)
	}
	repository, _, _ := strings.Cut(image, "@")
	return repository + "@" + digest, nil
}

func verifyImage(ctx context.Context, image, arch string) (err error) {
	if arch != runtime.GOARCH {
		return errors.New("image verification requires a native architecture runner")
	}
	output, err := docker(ctx, "image", "inspect", "--", image)
	if err != nil {
		return err
	}
	var info []struct {
		OS           string `json:"Os"`
		Architecture string `json:"Architecture"`
		Config       struct {
			Entrypoint []string `json:"Entrypoint"`
		} `json:"Config"`
	}
	if err = json.Unmarshal(output, &info); err != nil {
		return fmt.Errorf("decode image configuration: %w", err)
	}
	if len(info) != 1 || info[0].OS != platformOS || info[0].Architecture != arch {
		return errors.New("image configuration does not match requested platform")
	}
	if len(info[0].Config.Entrypoint) == 0 || info[0].Config.Entrypoint[0] == "" {
		return errors.New("image has no entrypoint")
	}
	dir, err := os.MkdirTemp("", "loco-image-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	output, err = docker(ctx, "create", "--", image)
	if err != nil {
		return err
	}
	container := strings.TrimSpace(string(output))
	defer func() {
		_, cleanupErr := docker(context.WithoutCancel(ctx), "rm", "--", container)
		err = errors.Join(err, cleanupErr)
	}()
	binary := filepath.Join(dir, "entrypoint")
	if _, err = docker(ctx, "cp", "--", container+":"+info[0].Config.Entrypoint[0], binary); err != nil {
		return err
	}
	return verifyBinary(binary, arch)
}

func verifyBinary(path, arch string) error {
	file, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("read ELF entrypoint: %w", err)
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB {
		return errors.New("entrypoint must be a little-endian ELF64 binary")
	}
	if file.Machine != machineByArch[arch] {
		return fmt.Errorf("entrypoint machine %s does not match %s", file.Machine, arch)
	}
	if len(file.Progs) == 0 {
		return errors.New("invalid ELF program headers")
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return errors.New("platform entrypoint must be statically linked")
		}
	}
	return nil
}

func docker(ctx context.Context, args ...string) ([]byte, error) {
	//nolint:gosec // Callers pass fixed subcommands and delimit operands.
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}
