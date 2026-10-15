package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/resource"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/sourcepack"
)

var (
	errImageTarget = errors.New(
		"--image requires selecting exactly one service; omit the name only for a single-service environment",
	)
	errEmptyImage       = errors.New("image reference must not be empty")
	errServiceNotInFile = errors.New("the file does not run the service in this environment")
	errServiceHasImage  = errors.New("the service runs a public image and is not built")
)

type buildTarget struct {
	name       string
	dockerfile string
	context    string
}

func BuildDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [service...]",
		Short: "Build and deploy the services of loco.yaml",
		Long: `Build the source services of loco.yaml on Loco and apply the file to an environment
with the builds.

The directory that holds loco.yaml is packed once and uploaded for every build; each
service builds with its own dockerfile and context. The archive honors the .dockerignore in
that directory, keeps every Dockerfile it builds, and never contains .git, .env or .env.*
files. A new source service is created first so it can be built; every other change of the
plan is applied with the builds. Ctrl-C while a build runs detaches without canceling it.
A new service whose only plan errors are the secrets it declares is created without being
built or deployed; the command prints the loco env set command for its secrets and exits
with an error, and the next deploy builds and deploys it.
Image services are applied without a build. Without service names, every source service the
file runs in the environment is built.

--image overrides one service's image for this deployment without editing loco.yaml.
Name exactly one service, or omit its name if the environment runs only one service.
This skips all source builds and uploads. The whole file is still planned and applied;
unrelated services can change, and new source services may still need an initial build.
The API resolves image tags and digests before confirmation.

The plan is shown before anything changes. Destructive operations need
--confirm-destructive and imports need --confirm-import. Without a terminal, --yes
replaces the prompt.

Examples:
  loco deploy
  loco deploy --env staging --yes
  loco deploy web worker --file deploy/loco.yaml
  loco deploy web --image ghcr.io/acme/web:release --yes`,
		RunE: runDeploy,
	}
	addTargetFlags(cmd)
	cmd.Flags().String("image", "", "Deploy one service from a public image reference without building source")
	cmd.Flags().StringP("file", "f", locofile.FileName, "Path to loco.yaml")
	cmd.Flags().BoolP("yes", "y", false, "Deploy without asking for confirmation")
	cmd.Flags().Bool("confirm-destructive", false, confirmDestructiveUsage)
	cmd.Flags().Bool("confirm-import", false, confirmImportUsage)
	return cmd
}

func runDeploy(cmd *cobra.Command, names []string) error {
	confirm, err := readConfirmations(cmd)
	if err != nil {
		return err
	}
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("error reading file flag: %w", err)
	}
	file, data, err := loadLocoFile(filePath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	t, err := resolveTarget(ctx, cmd)
	if err != nil {
		return err
	}
	services, err := locofile.Resolve(file, t.environmentName)
	if err != nil {
		return fmt.Errorf("%s does not resolve for environment %s: %w", filePath, t.environmentName, err)
	}
	var targets []buildTarget
	if cmd.Flags().Changed("image") {
		image, flagErr := cmd.Flags().GetString("image")
		if flagErr != nil {
			return fmt.Errorf("read image flag: %w", flagErr)
		}
		data, err = imageOverride(file, services, names, t.environmentName, image)
		if err != nil {
			return err
		}
		file, err = locofile.Parse(data)
		if err != nil {
			return err
		}
		services, err = locofile.Resolve(file, t.environmentName)
		if err != nil {
			return err
		}
	} else {
		targets, err = buildTargets(services, names)
		if err != nil {
			return err
		}
	}

	plan, err := requestPlan(ctx, t, data)
	if err != nil {
		return err
	}
	if len(plan.GetErrors()) > 0 {
		if handled, provisionErr := provisionForSecrets(ctx, t, data, plan, services, confirm, os.Stdout); handled {
			return provisionErr
		}
		return planErrorsError(filePath, plan.GetErrors())
	}
	if writeErr := writePlan(os.Stdout, filePath, plan); writeErr != nil {
		return writeErr
	}
	unconfirmed := unconfirmedOperations(plan.GetOperations(), confirm)
	if len(unconfirmed) > 0 {
		return refusalError(&planv1.ApplyRefusal{Unconfirmed: unconfirmed}, plan.GetRevision())
	}
	dir := filepath.Dir(filePath)
	archive, err := packTargets(dir, targets, os.Stdout)
	if err != nil {
		return err
	}
	if archive != nil {
		defer func() {
			if removeErr := archive.Remove(); removeErr != nil {
				fmt.Fprintf(os.Stdout, "Could not remove %s: %v\n", archive.Path, removeErr)
			}
		}()
	}
	if !confirm.yes {
		proceed, askErr := askToProceed("Deploy?")
		if askErr != nil {
			return askErr
		}
		if !proceed {
			fmt.Println("Deploy canceled.")
			return nil
		}
	}

	revision := plan.GetRevision()
	if provision := newTargets(plan.GetOperations(), targets); len(provision) > 0 {
		scope := applyScope{provision: provision}
		provisioned, applyErr := requestApply(ctx, t, data, revision, plan.GetImages(), confirm, scope)
		if applyErr != nil {
			return applyErr
		}
		fmt.Fprintf(os.Stdout, "Created %s before building.\n", strings.Join(provision, ", "))
		revision = provisioned.GetRevision()
	}

	builds, err := buildServices(ctx, t, archive, targets, os.Stdout)
	if err != nil {
		return err
	}
	applied, err := requestApply(ctx, t, data, revision, plan.GetImages(), confirm, applyScope{builds: builds})
	if err != nil {
		return err
	}
	if writeErr := writeApplied(os.Stdout, applied); writeErr != nil {
		return writeErr
	}
	return writeReachability(os.Stdout, services)
}

func imageOverride(
	file *locofile.File,
	services map[string]locofile.Service,
	names []string,
	environment, image string,
) ([]byte, error) {
	if strings.TrimSpace(image) == "" {
		return nil, errEmptyImage
	}
	if len(names) == 0 && len(services) == 1 {
		names = slices.Collect(maps.Keys(services))
	}
	if len(names) != 1 {
		return nil, errImageTarget
	}
	if _, exists := services[names[0]]; !exists {
		return nil, fmt.Errorf("%w: %s", errServiceNotInFile, names[0])
	}
	return locofile.OverrideImage(file, names[0], environment, image)
}

func buildTargets(services map[string]locofile.Service, names []string) ([]buildTarget, error) {
	if len(names) == 0 {
		for _, name := range slices.Sorted(maps.Keys(services)) {
			if services[name].Image == "" {
				names = append(names, name)
			}
		}
	}
	targets := make([]buildTarget, 0, len(names))
	for _, name := range names {
		service, inFile := services[name]
		if !inFile {
			return nil, fmt.Errorf("%w: %s", errServiceNotInFile, name)
		}
		if service.Image != "" {
			return nil, fmt.Errorf("%w: %s", errServiceHasImage, name)
		}
		targets = append(targets, buildTarget{
			name:       name,
			dockerfile: firstSet(service.Dockerfile, locofile.DefaultDockerfile),
			context:    firstSet(service.Context, locofile.DefaultContext),
		})
	}
	return targets, nil
}

func firstSet(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func newTargets(operations []*planv1.PlanOperation, targets []buildTarget) []string {
	var created []string
	for _, op := range operations {
		if op.GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE {
			continue
		}
		if slices.ContainsFunc(targets, func(target buildTarget) bool { return target.name == op.GetService() }) {
			created = append(created, op.GetService())
		}
	}
	return created
}

func packTargets(dir string, targets []buildTarget, out io.Writer) (*sourcepack.Archive, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	dockerfiles := make([]string, 0, len(targets))
	for _, target := range targets {
		dockerfiles = append(dockerfiles, path.Join(target.context, target.dockerfile))
	}
	archive, err := sourcepack.Pack(dir, dockerfiles)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", dir, err)
	}
	resource.PackedArchive(out, archive, dir)
	return archive, nil
}

func buildServices(
	ctx context.Context,
	t target,
	archive *sourcepack.Archive,
	targets []buildTarget,
	out io.Writer,
) (map[string]string, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	token := t.token
	builder := resource.NewSourceBuilder(t.host, token, out)
	builds := make(map[string]string, len(targets))
	for _, target := range targets {
		res, lookupErr := t.lookupResource(ctx, target.name)
		if lookupErr != nil {
			return nil, lookupErr
		}
		fmt.Fprintf(out, "Building %s from %s in %s\n", target.name, target.dockerfile, target.context)
		build, buildErr := builder.Build(ctx, resource.BuildInput{
			ResourceID:  res.GetId(),
			WorkspaceID: t.workspaceID,
			Archive:     archive,
			Dockerfile:  target.dockerfile,
			Context:     target.context,
		})
		if buildErr != nil {
			return nil, buildErr
		}
		builds[target.name] = build.GetId()
	}
	return builds, nil
}

func (t target) lookupResource(ctx context.Context, name string) (*resourcev1.Resource, error) {
	nameKey := &resourcev1.GetResourceNameKey{WorkspaceId: t.workspaceID, Name: name}
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{NameKey: nameKey},
	}

	resp, err := t.resourceClient().GetResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get service %s: %w", name, err)
	}
	return resp.GetResource(), nil
}

func writeReachability(w io.Writer, services map[string]locofile.Service) error {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(services)) {
		if domains := services[name].Domains; len(domains) > 0 {
			fmt.Fprintf(&b, "%s: https://%s\n", name, domains[0])
			continue
		}
		fmt.Fprintf(&b, "%s has no public URL and takes no internet traffic.\n", name)
	}
	_, err := fmt.Fprint(w, b.String())
	return err
}
