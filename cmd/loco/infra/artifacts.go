package infra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	"github.com/team-loco/loco/internal/docker"
	"github.com/team-loco/loco/internal/httputil"
	definition "github.com/team-loco/loco/internal/infra"
	loco "github.com/team-loco/loco/sdk/go"
)

type buildArtifact struct {
	Version       int                      `json:"version"`
	Manifest      loco.Manifest            `json:"manifest"`
	SourceDigest  string                   `json:"sourceDigest"`
	Reviewed      bool                     `json:"reviewed"`
	ExcludedPaths []string                 `json:"excludedPaths"`
	Images        map[string]imageArtifact `json:"images"`
}

type imageArtifact struct {
	Reference string `json:"reference"`
	Archive   string `json:"archive,omitempty"`
	Checksum  string `json:"checksum,omitempty"`
}

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "build", Short: "Build image archives without Loco credentials", RunE: exportImages}
	definitionFlags(cmd)
	cmd.Flags().String("manifest", "", "Previously evaluated authoring manifest")
	cmd.Flags().String("export-dir", "", "Output directory for image archives")
	cmd.Flags().Bool("reviewed", false, "Require clean tracked Git inputs")
	cmd.Flags().String("service", "", "Build one service")
	return cmd
}

func artifactDefinition(cmd *cobra.Command) (*definition.Definition, error) {
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return nil, err
	}
	root, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return definition.Discover(cwd, file, root)
}

func exportImages(cmd *cobra.Command, _ []string) error {
	module, err := artifactDefinition(cmd)
	if err != nil {
		return err
	}
	manifestPath, err := cmd.Flags().GetString("manifest")
	if err != nil {
		return err
	}
	output, err := cmd.Flags().GetString("export-dir")
	if err != nil {
		return err
	}
	if manifestPath == "" || output == "" {
		return fmt.Errorf("--manifest and --export-dir are required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest loco.Manifest
	if decodeErr := loco.Decode(bytes.NewReader(data), &manifest); decodeErr != nil {
		return decodeErr
	}
	if normalizeErr := loco.Normalize(&manifest); normalizeErr != nil {
		return normalizeErr
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	manifestPath, err = filepath.Abs(manifestPath)
	if err != nil {
		return err
	}
	reviewed, err := cmd.Flags().GetBool("reviewed")
	if err != nil {
		return err
	}
	selector, err := cmd.Flags().GetString("service")
	if err != nil {
		return err
	}
	excluded := make([]string, 0, 2)
	for _, path := range []string{output, manifestPath} {
		relative, relativeErr := filepath.Rel(module.ProjectRoot, path)
		if relativeErr != nil {
			return relativeErr
		}
		excluded = append(excluded, filepath.ToSlash(relative))
	}
	artifact := buildArtifact{
		Version:       1,
		Manifest:      manifest,
		Reviewed:      reviewed,
		ExcludedPaths: excluded,
		Images:        make(map[string]imageArtifact),
	}
	if err = definition.ValidateSourceDefinition(cmd.Context(), module, reviewed); err != nil {
		return err
	}
	artifact.SourceDigest, err = definition.SourceDigest(
		cmd.Context(),
		module.ProjectRoot,
		artifact.ExcludedPaths,
		reviewed,
	)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(output); statErr == nil {
		return fmt.Errorf("export directory already exists")
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if mkdirErr := os.MkdirAll(output, 0o700); mkdirErr != nil {
		return mkdirErr
	}
	buildModule := module
	if reviewed {
		snapshot, cleanup, snapshotErr := definition.Snapshot(cmd.Context(), module)
		if snapshotErr != nil {
			return snapshotErr
		}
		defer cleanup()
		buildModule = snapshot
	}
	found := selector == ""
	for _, service := range manifest.Stack.Services {
		if selector != "" && selector != service.Key {
			continue
		}
		found = true
		result, buildErr := exportImage(cmd, buildModule, output, service)
		if buildErr != nil {
			return fmt.Errorf("service %q: %w", service.Key, buildErr)
		}
		artifact.Images[service.Key] = result
	}
	if !found {
		return fmt.Errorf("service key %q is not declared", selector)
	}
	after, err := definition.SourceDigest(cmd.Context(), module.ProjectRoot, artifact.ExcludedPaths, reviewed)
	if err != nil {
		return err
	}
	if after != artifact.SourceDigest {
		return fmt.Errorf("source changed while building")
	}
	return writeJSON(filepath.Join(output, "build.json"), artifact)
}

func exportImage(
	cmd *cobra.Command,
	module *definition.Definition,
	output string,
	service loco.Service,
) (imageArtifact, error) {
	if service.Build == nil && strings.Contains(service.Image, "@sha256:") {
		return imageArtifact{Reference: service.Image}, nil
	}
	var request *definition.BuildRequest
	var err error
	if service.Build != nil {
		request, err = definition.ResolveBuild(module.ProjectRoot, service.Build)
		if err != nil {
			return imageArtifact{}, err
		}
	}
	engine, err := docker.NewClient(request)
	if err != nil {
		return imageArtifact{}, err
	}
	defer engine.Close()
	engine.GenerateImageTag("loco-build/"+service.Key, "", "", service.Key)
	logf := func(message string) { fmt.Fprintln(cmd.ErrOrStderr(), message) }
	if request != nil {
		if buildErr := engine.BuildImage(cmd.Context(), logf); buildErr != nil {
			return imageArtifact{}, buildErr
		}
	} else {
		if pullErr := engine.PullImage(cmd.Context(), service.Image, logf); pullErr != nil {
			return imageArtifact{}, pullErr
		}
		if tagErr := engine.ImageTag(cmd.Context(), service.Image); tagErr != nil {
			return imageArtifact{}, tagErr
		}
	}
	if validateErr := engine.ValidateImage(cmd.Context(), engine.ImageName, logf); validateErr != nil {
		return imageArtifact{}, validateErr
	}
	name := service.Key + ".tar"
	file, err := os.OpenFile(filepath.Join(output, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return imageArtifact{}, err
	}
	hash := sha256.New()
	saveErr := engine.SaveImage(cmd.Context(), io.MultiWriter(file, hash))
	closeErr := file.Close()
	if saveErr != nil {
		return imageArtifact{}, saveErr
	}
	if closeErr != nil {
		return imageArtifact{}, closeErr
	}
	return imageArtifact{Reference: engine.ImageName, Archive: name, Checksum: hex.EncodeToString(hash.Sum(nil))}, nil
}

func newPublishCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish reviewed image archives without running project code",
		RunE:  publishImages,
	}
	targetFlags(cmd)
	definitionFlags(cmd)
	cmd.Flags().String("artifact", "", "Build artifact JSON file")
	cmd.Flags().String("out", "", "Output image digest bindings JSON file")
	return cmd
}

func publishImages(cmd *cobra.Command, _ []string) error {
	selected, err := ResolveTarget(cmd)
	if err != nil {
		return err
	}
	module, err := artifactDefinition(cmd)
	if err != nil {
		return err
	}
	file, err := cmd.Flags().GetString("artifact")
	if err != nil {
		return err
	}
	out, err := cmd.Flags().GetString("out")
	if err != nil {
		return err
	}
	if file == "" || out == "" {
		return fmt.Errorf("--artifact and --out are required")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var artifact buildArtifact
	if decodeErr := loco.Decode(bytes.NewReader(data), &artifact); decodeErr != nil {
		return decodeErr
	}
	if artifact.Version != 1 {
		return fmt.Errorf("unsupported build artifact version")
	}
	if normalizeErr := loco.Normalize(&artifact.Manifest); normalizeErr != nil {
		return normalizeErr
	}
	digest, err := definition.SourceDigest(cmd.Context(), module.ProjectRoot, artifact.ExcludedPaths, artifact.Reviewed)
	if err != nil {
		return err
	}
	if digest != artifact.SourceDigest {
		return fmt.Errorf("source differs from the build artifact")
	}
	stack, err := cmd.Flags().GetString("stack")
	if err != nil {
		return err
	}
	if stack != "" && stack != artifact.Manifest.Stack.Name {
		return fmt.Errorf("selected stack differs from the build artifact")
	}
	host, err := url.Parse(selected.Host)
	if err != nil || host.Host == "" {
		return fmt.Errorf("invalid API host")
	}
	api := registryv1connect.NewRegistryServiceClient(httputil.NewHTTPClient(), selected.Host)
	bindings := make(map[string]string)
	for _, service := range artifact.Manifest.Stack.Services {
		built, exists := artifact.Images[service.Key]
		if !exists {
			continue
		}
		image, publishErr := publishArchive(
			cmd,
			selected,
			api,
			host.Host,
			filepath.Dir(file),
			artifact.Manifest.Stack.Name,
			service.Key,
			built,
		)
		if publishErr != nil {
			return fmt.Errorf("service %q: %w", service.Key, publishErr)
		}
		bindings[service.Key] = image
	}
	if len(bindings) == 0 {
		return fmt.Errorf("build artifact contains no declared images")
	}
	return writeJSON(out, bindings)
}

func publishArchive(
	cmd *cobra.Command,
	selected *Target,
	api registryv1connect.RegistryServiceClient,
	host, directory, stack, key string,
	built imageArtifact,
) (string, error) {
	if built.Archive == "" {
		if !regexp.MustCompile(`^\S+@sha256:[a-f0-9]{64}$`).MatchString(built.Reference) {
			return "", fmt.Errorf("external image must be pinned by digest")
		}
		return built.Reference, nil
	}
	if built.Archive != key+".tar" {
		return "", fmt.Errorf("unexpected image archive path")
	}
	info, err := os.Lstat(filepath.Join(directory, built.Archive))
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return "", fmt.Errorf("image archive must be a regular file smaller than 2 GiB")
	}
	file, err := os.Open(filepath.Join(directory, built.Archive))
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		return "", copyErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != built.Checksum {
		return "", fmt.Errorf("image archive checksum mismatch")
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		return "", seekErr
	}
	engine, err := docker.NewClient(nil)
	if err != nil {
		return "", err
	}
	defer engine.Close()
	logf := func(message string) { fmt.Fprintln(cmd.ErrOrStderr(), message) }
	if loadErr := engine.LoadImage(cmd.Context(), file, logf); loadErr != nil {
		return "", loadErr
	}
	return publishLoaded(cmd.Context(), engine, selected, api, host, stack, key, built.Reference, logf)
}

func publishLoaded(
	ctx context.Context,
	engine *docker.DockerClient,
	selected *Target,
	api registryv1connect.RegistryServiceClient,
	host, stack, key, reference string,
	logf func(string),
) (string, error) {
	request := connect.NewRequest(
		&registryv1.GetImageRepositoryRequest{EnvironmentId: selected.EnvironmentID, StackName: stack, ServiceKey: key},
	)
	request.Header().Set("Authorization", "Bearer "+selected.Token)
	response, err := api.GetImageRepository(ctx, request)
	if err != nil {
		return "", err
	}
	expected := "loco/" + selected.EnvironmentID + "/" + stack + "/" + key
	if response.Msg.GetPushRepository() != expected {
		return "", fmt.Errorf("registry returned an unexpected publishing target")
	}
	engine.GenerateImageTag(host+"/"+expected, "", selected.WorkspaceID, key)
	engine.SetRegistry(host)
	if tagErr := engine.ImageTag(ctx, reference); tagErr != nil {
		return "", tagErr
	}
	if validateErr := engine.ValidateImage(ctx, engine.ImageName, logf); validateErr != nil {
		return "", validateErr
	}
	if pushErr := engine.PushImage(ctx, logf, "loco", selected.Token); pushErr != nil {
		return "", pushErr
	}
	digest, err := engine.ImageDigest(ctx, engine.ImageName)
	if err != nil {
		return "", err
	}
	return response.Msg.GetRepository() + "@" + digest, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
