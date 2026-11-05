package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
	"go.yaml.in/yaml/v2"
)

const (
	verificationPort          int32 = 8080
	verificationPoll                = 2 * time.Second
	verificationCleanup             = time.Minute
	verificationResponseLimit       = 4096
	verificationLogSeconds          = 1
	verificationSecretName          = "LOCO_VERIFY_SECRET" //nolint:gosec // Application environment key, not a credential.
)

var (
	errVerification       = errors.New("installation source verification failed")
	errVerificationTarget = errors.New(
		"verification requires a build-enabled worker and an authorized environment of the same tier",
	)
	errVerificationCleanup = errors.New("verification resource cleanup failed")
)

type VerificationTarget struct {
	Worker        string
	EnvironmentID string
}

// VerifySource creates and deletes only its own disposable resource in the selected environment.
func VerifySource(
	ctx context.Context,
	config Config,
	manifestPath, token string,
	target VerificationTarget,
) (Status, error) {
	status, err := StatusInstallation(ctx, config, token)
	if err != nil {
		return Status{}, err
	}
	if status.Phase != "running" {
		return Status{}, errVerificationTarget
	}
	var worker Worker
	for _, candidate := range config.Workers {
		if candidate.Name == target.Worker {
			worker = candidate
		}
	}
	if worker.Name == "" || !worker.Builds.Enabled {
		return Status{}, errVerificationTarget
	}
	clients := rpc.ForHost(config.ControlPlane.APIURL, token)
	environment, lookupErr := clients.Environment.GetEnvironment(
		ctx,
		&environmentv1.GetEnvironmentRequest{EnvironmentId: target.EnvironmentID},
	)
	if lookupErr != nil {
		return Status{}, errVerificationTarget
	}
	var tier string
	switch environment.GetEnvironment().GetType() {
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING:
		tier = "staging"
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION:
		tier = "production"
	default:
		return Status{}, errVerificationTarget
	}
	if tier != worker.Tier {
		return Status{}, errVerificationTarget
	}
	release, loadErr := loadArtifacts(ctx, manifestPath)
	if loadErr != nil {
		return Status{}, loadErr
	}
	base, baseErr := verificationBase(release.worker)
	if baseErr != nil {
		return Status{}, baseErr
	}
	if err := verifyApplication(ctx, clients, worker, environment.GetEnvironment(), base); err != nil {
		return Status{}, err
	}
	status.Phase = "verified"
	return status, nil
}

func verifyApplication(
	ctx context.Context,
	clients *rpc.Clients,
	worker Worker,
	environment *environmentv1.Environment,
	base string,
) (err error) {
	name := "verify-" + uuid.NewString()
	marker := "loco-" + uuid.NewString()
	secret := rand.Text()
	secretDigest := sha256.Sum256([]byte(secret))
	secretProof := hex.EncodeToString(secretDigest[:])
	domain := name + "." + worker.AppsDomain
	configuration, configErr := clients.Config.GetConfig(ctx, &configv1.GetConfigRequest{})
	if configErr != nil {
		return errVerification
	}
	defaults := configuration.GetServiceDefaults()
	file := locofile.File{Version: locofile.Version, Partial: name, Services: map[string]locofile.Service{name: {
		Dockerfile: locofile.DefaultDockerfile,
		Port:       new(verificationPort),
		Domains:    []string{domain},
		Secrets:    []string{verificationSecretName},
		Health: &locofile.Health{
			Path: "/",
		},
		Regions: map[string]locofile.Region{
			worker.Region: {
				CPU:      defaults.GetCpu(),
				Memory:   defaults.GetMemory(),
				Replicas: &locofile.Replicas{Min: new(defaults.GetMinReplicas()), Max: new(defaults.GetMaxReplicas())},
			},
		},
	}}}
	data, marshalErr := json.Marshal(file)
	if marshalErr != nil {
		return errVerification
	}
	plan, planErr := clients.Plan.Plan(ctx, &planv1.PlanRequest{File: data, EnvironmentId: environment.GetId()})
	if planErr != nil {
		return errVerification
	}
	var resourceID string
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), verificationCleanup)
		defer cancel()
		if resourceID == "" {
			resource, lookupErr := clients.Resource.GetResource(
				cleanup,
				verificationResourceKey(environment.GetWorkspaceId(), name),
			)
			if connect.CodeOf(lookupErr) == connect.CodeNotFound {
				return
			}
			if lookupErr != nil || resource.GetResource().GetPartial() != name {
				err = errors.Join(err, errVerificationCleanup)
				return
			}
			resourceID = resource.GetResource().GetId()
		}
		if _, cleanupErr := clients.Resource.DeleteResource(
			cleanup,
			&resourcev1.DeleteResourceRequest{ResourceId: resourceID},
		); cleanupErr != nil {
			err = errors.Join(err, errVerificationCleanup)
		}
	}()
	if _, provisionErr := clients.Plan.Apply(ctx, &planv1.ApplyRequest{
		File:          data,
		EnvironmentId: environment.GetId(),
		Revision:      plan.GetRevision(),
		Images:        plan.GetImages(),
		Provision:     []string{name},
	}); provisionErr != nil {
		return errVerification
	}
	resource, lookupErr := clients.Resource.GetResource(
		ctx,
		verificationResourceKey(environment.GetWorkspaceId(), name),
	)
	if lookupErr != nil || resource.GetResource().GetPartial() != name {
		return errVerification
	}
	resourceID = resource.GetResource().GetId()
	if _, setErr := clients.Secret.SetSecrets(ctx, &secretv1.SetSecretsRequest{
		ResourceId:    resourceID,
		EnvironmentId: environment.GetId(),
		Values:        map[string]string{verificationSecretName: secret},
	}); setErr != nil {
		return fmt.Errorf("verify encrypted secret storage: %w", errVerification)
	}
	archive, archiveErr := verificationArchive(base, marker)
	if archiveErr != nil {
		return archiveErr
	}
	build, buildErr := clients.Build.CreateBuild(ctx, &buildv1.CreateBuildRequest{
		ResourceId: resourceID, DockerfilePath: locofile.DefaultDockerfile, SourceSize: int64(len(archive)),
	})
	if buildErr != nil {
		return errVerification
	}
	if uploadErr := verificationUpload(ctx, build.GetUploadUrl(), archive); uploadErr != nil {
		return uploadErr
	}
	if _, startErr := clients.Build.StartBuild(
		ctx,
		&buildv1.StartBuildRequest{BuildId: build.GetBuildId()},
	); startErr != nil {
		return errVerification
	}
	if waitErr := verificationBuild(ctx, clients, build.GetBuildId()); waitErr != nil {
		return waitErr
	}
	plan, planErr = clients.Plan.Plan(ctx, &planv1.PlanRequest{File: data, EnvironmentId: environment.GetId()})
	if planErr != nil || len(plan.GetErrors()) > 0 {
		return errVerification
	}
	if _, applyErr := clients.Plan.Apply(ctx, &planv1.ApplyRequest{
		File: data, EnvironmentId: environment.GetId(), Revision: plan.GetRevision(), Images: plan.GetImages(),
		Builds: map[string]string{name: build.GetBuildId()},
	}); applyErr != nil {
		return errVerification
	}
	if probeErr := verificationProbe(ctx, "https://"+domain, marker); probeErr != nil {
		return probeErr
	}
	if probeErr := verificationProbe(ctx, "https://"+domain+"/cgi-bin/proof", secretProof); probeErr != nil {
		return fmt.Errorf("verify deployed secret delivery: %w", errVerification)
	}
	request := &observabilityv1.QueryLogsRequest{
		WorkspaceId: environment.GetWorkspaceId(), EnvironmentId: environment.GetId(), Search: marker,
		ResourceIds: []string{resourceID}, Regions: []string{worker.Region},
	}
	if logsErr := verificationLogs(ctx, clients, request, marker); logsErr != nil {
		return logsErr
	}
	return nil
}

func verificationResourceKey(workspace, name string) *resourcev1.GetResourceRequest {
	return &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{WorkspaceId: workspace, Name: name},
		},
	}
}

func verificationBase(chart []byte) (string, error) {
	files, err := archiveFiles(chart)
	if err != nil {
		return "", err
	}
	var values struct {
		Collector struct {
			InitContainers []struct {
				Image string `yaml:"image"`
			} `yaml:"initContainers"`
		} `yaml:"otel-col-daemon"`
	}
	if decodeErr := yaml.Unmarshal(files["loco-worker/values.yaml"], &values); decodeErr != nil {
		return "", errInvalidRelease
	}
	if len(values.Collector.InitContainers) == 0 {
		return "", errInvalidRelease
	}
	image := values.Collector.InitContainers[0].Image
	if !digestReference.MatchString(image) {
		return "", errInvalidRelease
	}
	return image, nil
}

func verificationArchive(base, marker string) ([]byte, error) {
	files := map[string]string{
		"Dockerfile": fmt.Sprintf(
			"FROM %s\nCOPY --chmod=0444 index.html /www/index.html\n"+
				"COPY --chmod=0555 proof /www/cgi-bin/proof\nUSER 65534\n"+
				"CMD [\"sh\",\"-c\",\"while :; do echo %s; sleep %d; done & exec httpd -f -p %d -h /www\"]\n",
			base, marker, verificationLogSeconds, verificationPort,
		),
		"index.html": marker,
		"proof": "#!/bin/sh\nprintf 'Content-Type: text/plain\\r\\n\\r\\n'\n" +
			"printf %s \"$" + verificationSecretName + "\" | sha256sum | cut -d' ' -f1 | tr -d '\\n'\n",
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	writer := tar.NewWriter(compressed)
	for name, data := range files {
		header := &tar.Header{Name: name, Size: int64(len(data)), Mode: 0600}
		if err := writer.WriteHeader(header); err != nil {
			return nil, errVerification
		}
		if _, err := io.WriteString(writer, data); err != nil {
			return nil, errVerification
		}
	}
	if err := writer.Close(); err != nil {
		return nil, errVerification
	}
	if err := compressed.Close(); err != nil {
		return nil, errVerification
	}
	return archive.Bytes(), nil
}

func verificationUpload(ctx context.Context, url string, data []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return errVerification
	}
	request.Header.Set("Content-Type", "application/gzip")
	response, err := httputil.NewHTTPClient().Do(request)
	if err != nil {
		return errVerification
	}
	closeErr := response.Body.Close()
	if closeErr != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errVerification
	}
	return nil
}

func verificationBuild(ctx context.Context, clients *rpc.Clients, id string) error {
	for {
		response, err := clients.Build.GetBuild(ctx, &buildv1.GetBuildRequest{BuildId: id})
		if err != nil {
			return errVerification
		}
		switch response.GetBuild().GetStatus() {
		case buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED:
			return nil
		case buildv1.BuildStatus_BUILD_STATUS_FAILED:
			return errVerification
		case buildv1.BuildStatus_BUILD_STATUS_CANCELED:
			return errVerification
		}
		if err := verificationWait(ctx); err != nil {
			return err
		}
	}
}

func verificationProbe(ctx context.Context, url, marker string) error {
	client := httputil.NewHTTPClient()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return errVerification
		}
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, verificationResponseLimit))
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				return errVerification
			}
			if response.StatusCode == http.StatusOK && string(body) == marker {
				return nil
			}
		}
		if err := verificationWait(ctx); err != nil {
			return err
		}
	}
}

func verificationLogs(
	ctx context.Context,
	clients *rpc.Clients,
	request *observabilityv1.QueryLogsRequest,
	marker string,
) error {
	for {
		response, err := clients.ObservabilityProxy.QueryLogs(ctx, request)
		if err != nil {
			return errVerification
		}
		for _, entry := range response.GetEntries() {
			if strings.Contains(entry.GetBody(), marker) {
				return nil
			}
		}
		if err := verificationWait(ctx); err != nil {
			return err
		}
	}
}

func verificationWait(ctx context.Context) error {
	timer := time.NewTimer(verificationPoll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errVerification
	case <-timer.C:
		return nil
	}
}
