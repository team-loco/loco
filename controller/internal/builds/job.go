package builds

import (
	"fmt"
	"path"
	"strconv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelBuildID          = "loco.io/build-id"
	labelComponent        = "app.kubernetes.io/component"
	componentBuild        = "build"
	buildContainerFetch   = "fetch"
	buildContainerBuild   = "build"
	buildContainerPush    = "push"
	buildVolumeWorkspace  = "workspace"
	buildVolumeCache      = "cache"
	buildVolumeOut        = "out"
	buildVolumeBuildkit   = "buildkit"
	buildVolumePushCreds  = "push-credentials"
	buildVolumePullCreds  = "pull-credentials"
	buildPushSecret       = "registry-push"
	buildPullSecret       = "registry-pull"
	buildWorkspaceDir     = "/workspace"
	buildCacheDir         = "/cache"
	buildOutDir           = "/out"
	buildImageDir         = "/out/image"
	buildCacheOutDir      = "/out/cache"
	buildkitStateDir      = "/home/user/.local/share/buildkit"
	buildPushCredsDir     = "/credentials/push"
	buildPullCredsDir     = "/credentials/pull"
	buildDockerConfigFile = "config.json"
	buildUser             = int64(1000)
	buildkitFlags         = "--oci-worker-no-process-sandbox"
	buildTerminationLog   = "/dev/termination-log"

	builderEnvSourceURL       = "SOURCE_URL"
	builderEnvDownloadTimeout = "DOWNLOAD_TIMEOUT"
)

func buildLabels(build *locov1alpha1.Build) map[string]string {
	return map[string]string{
		managed.LabelManagedBy:   managed.ManagedByValue,
		labelComponent:           componentBuild,
		managed.LabelWorkspaceID: build.Spec.WorkspaceID,
		managed.LabelResourceID:  build.Spec.ResourceID,
		labelBuildID:             build.Spec.BuildID,
	}
}

func buildImageRef(build *locov1alpha1.Build) string {
	return build.Spec.ImageRepository + ":" + locov1alpha1.BuildImageTagPrefix + build.Spec.BuildID
}

func buildCacheRef(build *locov1alpha1.Build) string {
	return build.Spec.ImageRepository + ":" + locov1alpha1.BuildCacheTagPrefix + build.Spec.BuildID
}

func emptyDirVolume(name string, size resource.Quantity) corev1.Volume {
	limit := size
	return corev1.Volume{
		Name:     name,
		EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &limit},
	}
}

func dockerConfigVolume(name, secretName string) corev1.Volume {
	items := []corev1.KeyToPath{{Key: corev1.DockerConfigJsonKey, Path: buildDockerConfigFile}}
	return corev1.Volume{
		Name: name,
		Secret: &corev1.SecretVolumeSource{
			SecretName:  secretName,
			Items:       items,
			DefaultMode: new(int32(0o440)),
		},
	}
}

func helperSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: new(false),
		ReadOnlyRootFilesystem:   new(true),
		RunAsNonRoot:             new(true),
		RunAsUser:                new(buildUser),
		RunAsGroup:               new(buildUser),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func buildkitSecurityContext(cfg *Config) *corev1.SecurityContext {
	seccomp := &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}
	if cfg.SeccompLocalhostProfile != "" {
		seccomp = &corev1.SeccompProfile{
			Type:             corev1.SeccompProfileTypeLocalhost,
			LocalhostProfile: new(cfg.SeccompLocalhostProfile),
		}
	}
	return &corev1.SecurityContext{
		RunAsNonRoot:    new(true),
		RunAsUser:       new(buildUser),
		RunAsGroup:      new(buildUser),
		SeccompProfile:  seccomp,
		AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined},
	}
}

func byteFlag(name string, value int64) string {
	return "--" + name + "=" + strconv.FormatInt(value, 10)
}

func (r *Reconciler) fetchContainer(build *locov1alpha1.Build) corev1.Container {
	cfg := &r.Config
	args := []string{
		"fetch",
		"--workspace=" + buildWorkspaceDir,
		"--dockerfile=" + path.Join(build.Spec.Context, build.Spec.DockerfilePath),
		byteFlag("max-source-bytes", cfg.MaxSourceSize.Value()),
		byteFlag("max-context-bytes", cfg.MaxContextSize.Value()),
		"--max-entries=" + strconv.Itoa(cfg.MaxContextFiles),
	}
	mounts := []corev1.VolumeMount{
		{Name: buildVolumeWorkspace, MountPath: buildWorkspaceDir},
		{Name: buildVolumeCache, MountPath: buildCacheDir},
	}
	env := []corev1.EnvVar{
		{Name: builderEnvSourceURL, Value: build.Spec.SourceURL},
		{Name: builderEnvDownloadTimeout, Value: cfg.DownloadTimeout.Duration.String()},
	}
	if build.Spec.CacheRef != "" {
		args = append(args,
			"--cache-dir="+buildCacheDir,
			"--cache-ref="+build.Spec.CacheRef,
			byteFlag("max-cache-bytes", cfg.Storage.Cache.Value()),
		)
	}
	if r.PullSecretName != "" {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      buildVolumePullCreds,
			MountPath: buildPullCredsDir,
			ReadOnly:  true,
		})
		env = append(env, corev1.EnvVar{Name: "DOCKER_CONFIG", Value: buildPullCredsDir})
	}
	if cfg.InsecureRegistry {
		args = append(args, "--insecure")
	}
	return corev1.Container{
		Name:                     buildContainerFetch,
		Image:                    cfg.BuilderImage.Reference(),
		ImagePullPolicy:          cfg.BuilderImage.PullPolicy,
		Args:                     args,
		Env:                      env,
		VolumeMounts:             mounts,
		Resources:                cfg.Resources.Fetch,
		SecurityContext:          helperSecurityContext(),
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
	}
}

func (r *Reconciler) buildkitContainer(build *locov1alpha1.Build) corev1.Container {
	cfg := &r.Config
	contextDir := path.Join(buildWorkspaceDir, build.Spec.Context)
	dockerfileDir := path.Join(contextDir, path.Dir(build.Spec.DockerfilePath))
	dockerfileName := path.Base(build.Spec.DockerfilePath)
	args := []string{
		"build",
		"--progress=plain",
		"--frontend=dockerfile.v0",
		"--local=context=" + contextDir,
		"--local=dockerfile=" + dockerfileDir,
		"--opt=filename=" + dockerfileName,
		"--output=type=oci,tar=false,dest=" + buildImageDir,
		"--export-cache=type=local,mode=max,dest=" + buildCacheOutDir,
		"--import-cache=type=local,src=" + buildCacheDir,
	}
	return corev1.Container{
		Name:            buildContainerBuild,
		Image:           cfg.BuildkitImage.Reference(),
		ImagePullPolicy: cfg.BuildkitImage.PullPolicy,
		Command:         []string{"buildctl-daemonless.sh"},
		Args:            args,
		Env:             []corev1.EnvVar{{Name: "BUILDKITD_FLAGS", Value: buildkitFlags}},
		VolumeMounts: []corev1.VolumeMount{
			{Name: buildVolumeWorkspace, MountPath: buildWorkspaceDir, ReadOnly: true},
			{Name: buildVolumeCache, MountPath: buildCacheDir, ReadOnly: true},
			{Name: buildVolumeOut, MountPath: buildOutDir},
			{Name: buildVolumeBuildkit, MountPath: buildkitStateDir},
		},
		Resources:                cfg.Resources.Build,
		SecurityContext:          buildkitSecurityContext(cfg),
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
	}
}

func (r *Reconciler) pushContainer(build *locov1alpha1.Build) corev1.Container {
	cfg := &r.Config
	args := []string{
		"push",
		"--image-dir=" + buildImageDir,
		"--cache-dir=" + buildCacheOutDir,
		"--image-ref=" + buildImageRef(build),
		"--cache-ref=" + buildCacheRef(build),
		byteFlag("max-image-bytes", cfg.MaxImageSize.Value()),
		byteFlag("max-cache-bytes", cfg.Storage.Cache.Value()),
		"--termination-log=" + buildTerminationLog,
	}
	if cfg.InsecureRegistry {
		args = append(args, "--insecure")
	}
	return corev1.Container{
		Name:            buildContainerPush,
		Image:           cfg.BuilderImage.Reference(),
		ImagePullPolicy: cfg.BuilderImage.PullPolicy,
		Args:            args,
		Env:             []corev1.EnvVar{{Name: "DOCKER_CONFIG", Value: buildPushCredsDir}},
		VolumeMounts: []corev1.VolumeMount{
			{Name: buildVolumeOut, MountPath: buildOutDir, ReadOnly: true},
			{Name: buildVolumePushCreds, MountPath: buildPushCredsDir, ReadOnly: true},
		},
		Resources:                cfg.Resources.Push,
		SecurityContext:          helperSecurityContext(),
		TerminationMessagePath:   buildTerminationLog,
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
	}
}

func (r *Reconciler) buildJob(build *locov1alpha1.Build) (*batchv1.Job, error) {
	cfg := &r.Config
	labels := buildLabels(build)
	volumes := []corev1.Volume{
		emptyDirVolume(buildVolumeWorkspace, cfg.Storage.Workspace),
		emptyDirVolume(buildVolumeCache, cfg.Storage.Cache),
		emptyDirVolume(buildVolumeOut, cfg.Storage.Out),
		emptyDirVolume(buildVolumeBuildkit, cfg.Storage.Buildkit),
		dockerConfigVolume(buildVolumePushCreds, buildPushSecret),
	}
	if r.PullSecretName != "" {
		pullVolume := dockerConfigVolume(buildVolumePullCreds, buildPullSecret)
		volumes = append(volumes, pullVolume)
	}
	var runtimeClassName *string
	if cfg.RuntimeClassName != "" {
		runtimeClassName = new(cfg.RuntimeClassName)
	}
	timeout := int64(cfg.Timeout.Seconds())
	ttl := int32(cfg.TTLAfterFinished.Seconds())

	fetch := r.fetchContainer(build)
	buildkit := r.buildkitContainer(build)
	push := r.pushContainer(build)
	podSecurity := &corev1.PodSecurityContext{
		RunAsNonRoot:   new(true),
		RunAsUser:      new(buildUser),
		RunAsGroup:     new(buildUser),
		FSGroup:        new(buildUser),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}

	job := &batchv1.Job{
		Name:      build.Name,
		Namespace: build.Namespace,
		Labels:    labels,
		Spec: batchv1.JobSpec{
			BackoffLimit:            new(int32(0)),
			ActiveDeadlineSeconds:   new(timeout),
			TTLSecondsAfterFinished: new(ttl),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: new(false),
					EnableServiceLinks:           new(false),
					SecurityContext:              podSecurity,
					InitContainers:               []corev1.Container{fetch, buildkit},
					Containers:                   []corev1.Container{push},
					Volumes:                      volumes,
					NodeSelector:                 cfg.NodeSelector,
					Tolerations:                  cfg.Tolerations,
					RuntimeClassName:             runtimeClassName,
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(build, job, r.Scheme); err != nil {
		return nil, fmt.Errorf("set owner of job %s: %w", build.Name, err)
	}
	return job, nil
}
