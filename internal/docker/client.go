package docker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"

	json "github.com/goccy/go-json"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/moby/go-archive"
	"github.com/team-loco/loco/shared/config"
)

// MINIMUM_DOCKER_ENGINE_VERSION is the lowest allowed docker version.
// should be the limited to the last major docker version
const (
	MINIMUM_DOCKER_ENGINE_VERSION = "28.0.0"
	GITLAB_REGISTRY_URL           = "registry.gitlab.com"
)

type DockerClient struct {
	dockerClient *client.Client
	cfg          *config.LoadedConfig
	registryUrl  string
	ImageName    string
}

func NewClient(cfg *config.LoadedConfig) (*DockerClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	v, err := cli.ServerVersion(context.Background())
	if err != nil {
		return nil, err
	}

	if v.Version < MINIMUM_DOCKER_ENGINE_VERSION {
		return nil, fmt.Errorf("loco requires minimum Docker engine version of %s. Please update your Docker version", MINIMUM_DOCKER_ENGINE_VERSION)
	}

	return &DockerClient{
		dockerClient: cli,
		cfg:          cfg,
		registryUrl:  GITLAB_REGISTRY_URL,
	}, nil
}

func (c *DockerClient) Close() error {
	if c.dockerClient != nil {
		return c.dockerClient.Close()
	}
	return nil
}

type Message struct {
	Stream string `json:"stream"`
	Status string `json:"status"`
	ID     string `json:"id"`
	Aux    struct {
		ID string `json:"ID"`
	} `json:"aux"`
}

func printDockerOutput(r io.Reader, logf func(string)) error {
	scanner := bufio.NewScanner(r)
	seenStatuses := make(map[string]string)

	for scanner.Scan() {
		var msg Message
		line := scanner.Text()

		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // skip unparseable lines
		}
		switch {
		case msg.Status != "":
			// Only log new, meaningful status changes (skip "Waiting", "Downloading", etc.)
			if msg.ID != "" {
				if prev, ok := seenStatuses[msg.ID]; ok && prev == msg.Status {
					continue
				}
				seenStatuses[msg.ID] = msg.Status
			}
			// only log certain messages, to reduce noise
			if strings.Contains(msg.Status, "Built") ||
				strings.Contains(msg.Status, "Pushed") ||
				strings.Contains(msg.Status, "Successfully") ||
				strings.Contains(msg.Status, "latest") {
				logf(msg.Status)
			}
		case msg.Stream != "":
			if strings.HasPrefix(msg.Stream, "Step") ||
				strings.HasPrefix(msg.Stream, "Successfully") {
				logf(strings.TrimSpace(msg.Stream))
			}
		case msg.Aux.ID != "":
			logf("Image ID: " + msg.Aux.ID)
		}
	}
	return scanner.Err()
}

func (c *DockerClient) BuildImage(ctx context.Context, logf func(string)) error {
	buildContext, err := archive.TarWithOptions(c.cfg.ProjectPath, &archive.TarOptions{})
	if err != nil {
		return err
	}
	defer buildContext.Close()

	slog.Debug("built docker context", slog.String("project", c.cfg.ProjectPath))
	relDockerfilePath, err := filepath.Rel(c.cfg.ProjectPath, c.cfg.Config.Build.DockerfilePath)
	if err != nil {
		return err
	}

	slog.Debug("dockerfile path", slog.String("path", relDockerfilePath), slog.String("imageName", c.ImageName))
	options := build.ImageBuildOptions{
		Tags:       []string{c.ImageName},
		Dockerfile: relDockerfilePath,
		Remove:     true, // remove intermediate containers
		Platform:   "linux/amd64",
		Version:    build.BuilderBuildKit,
	}
	// todo: should we have memory limits or similar for the build process?

	response, err := c.dockerClient.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return fmt.Errorf("build error: %v", err)
	}
	defer response.Body.Close()

	return printDockerOutput(response.Body, logf)
}

func (c *DockerClient) PushImage(ctx context.Context, logf func(string), username, password string) error {
	authConfig := registry.AuthConfig{
		Username:      username,
		Password:      password,
		ServerAddress: c.registryUrl,
	}

	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("error when encoding authConfig: %v", err)
	}

	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	pushOptions := image.PushOptions{
		RegistryAuth: authStr,
	}
	rc, err := c.dockerClient.ImagePush(ctx, c.ImageName, pushOptions)
	if err != nil {
		return fmt.Errorf("error when pushing image: %v", err)
	}
	defer rc.Close()

	return printDockerOutput(rc, logf)
}

func (c *DockerClient) ValidateImage(ctx context.Context, imageID string, logf func(string)) error {
	// placeholder implementation, i think we need to come back to this
	logf(fmt.Sprintf("Validating image: %s", imageID))
	_, err := c.dockerClient.ImageInspect(ctx, imageID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return fmt.Errorf("image %q not found locally", imageID)
		}
		return fmt.Errorf("failed to inspect image %q: %w", imageID, err)
	}
	logf(fmt.Sprintf("Image %q found locally", imageID))
	return nil
}

func (c *DockerClient) ImageTag(ctx context.Context, imageID string) error {
	return c.dockerClient.ImageTag(ctx, imageID, c.ImageName)
}

func (c *DockerClient) GenerateImageTag(imageBase string, orgID, workspaceID, appID int64) string {
	imageNameBase := imageBase
	var randSuffix string
	randBytes := make([]byte, 4)
	if _, err := rand.Read(randBytes); err != nil {
		slog.Warn("failed to generate random bytes, using timestamp fallback", "error", err)
		randSuffix = fmt.Sprintf("%08x", time.Now().UnixNano())
	} else {
		randSuffix = hex.EncodeToString(randBytes)
	}

	tag := fmt.Sprintf("org-%d-wks-%d-app-%d-%s", orgID, workspaceID, appID, randSuffix)

	if !strings.Contains(imageNameBase, ":") {
		imageNameBase += ":" + tag
	}
	return imageNameBase
}
