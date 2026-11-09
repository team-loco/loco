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
	"github.com/team-loco/loco/cmd/internal/sourcebuild"
	"github.com/team-loco/loco/cmd/internal/sourcepack"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/locofile"
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
		Short: "Build and deploy an application",
		Long: `Deploy from the current directory. If it has no loco.yaml, Loco deploys one
service using API defaults for the port, region, resources, and platform domain. It
turns the directory name into a safe service name and adds a four-letter suffix to
the public hostname. It uses Dockerfile in that directory for a source
build. In a terminal, it asks for the service name. No file is written. Set --name,
--port, --region, --domain, --dockerfile, or --context to override those choices;
--private omits the public domain. Use --yes without a terminal.

If loco.yaml exists, build its source services and apply the file to an environment
with the builds. The file's services and environments remain the source of truth.

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

--image deploys a public image without a source build. With loco.yaml, it overrides
one service's image for this deployment without editing the file. Name exactly one
service, or omit its name if the environment runs only one service. The whole file
is still planned and applied. The API resolves image tags and digests before
confirmation.

The plan is shown before anything changes. Destructive operations need
--confirm-destructive and imports need --confirm-import. Without a terminal, --yes
replaces the prompt.

Examples:
  loco deploy
  loco deploy --name web --port 8080 --yes
  loco deploy --name worker --private --image ghcr.io/acme/worker:release --yes
  loco deploy --env staging --yes
  loco deploy web worker --file deploy/loco.yaml
  loco deploy web --image ghcr.io/acme/web:release --yes`,
		RunE: runDeploy,
	}
	addTargetFlags(cmd)
	cmd.Flags().String("image", "", "Deploy from a public image reference without building source")
	cmd.Flags().StringP("file", "f", locofile.FileName, "Path to loco.yaml")
	cmd.Flags().String("name", "", "Service name when deploying without loco.yaml (defaults to the directory name)")
	cmd.Flags().Int32("port", 0, "Application port when deploying without loco.yaml (defaults to the API value)")
	cmd.Flags().String("region", "", "Region when deploying without loco.yaml (defaults to the API region)")
	cmd.Flags().String("domain", "", "Public hostname when deploying without loco.yaml")
	cmd.Flags().Bool("private", false, "Deploy without a public hostname")
	cmd.Flags().String("dockerfile", "", "Dockerfile path when deploying without loco.yaml")
	cmd.Flags().String("context", "", "Build context when deploying without loco.yaml")
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
	file, data, quickDeploy, err := loadDeployFile(cmd, filePath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	t, err := resolveTarget(ctx, cmd)
	if err != nil {
		return err
	}
	if quickDeploy {
		file, data, err = buildQuickDeploy(cmd, names, t)
		if err != nil {
			return err
		}
		filePath = "current directory"
		names = nil
	}
	environment := t.Environment.GetName()
	services, err := resolveServices(file, filePath, environment)
	if err != nil {
		return err
	}
	services, targets, data, err := deployTargets(cmd, file, services, data, names, environment, quickDeploy)
	if err != nil {
		return err
	}

	resolved := func() (map[string]locofile.Service, error) { return services, nil }
	plan, err := reviewPlan(ctx, cmd, t, filePath, data, resolved, confirm)
	if err != nil {
		return err
	}
	var archive *sourcepack.Archive
	if len(targets) > 0 {
		archive, err = packTargets(filepath.Dir(filePath), targets, os.Stdout)
		if err != nil {
			return err
		}
		defer func() {
			if removeErr := archive.Remove(); removeErr != nil {
				fmt.Fprintf(os.Stdout, "Could not remove %s: %v\n", archive.Path, removeErr)
			}
		}()
	}
	if proceed, askErr := cmdutil.Confirm(cmd, "Deploy?"); !proceed {
		return askErr
	}

	revision := plan.GetRevision()
	if provision := newTargets(plan.GetOperations(), targets); len(provision) > 0 {
		provisioned, applyErr := requestApply(ctx, applyRequest{
			target:    t,
			file:      data,
			revision:  revision,
			images:    plan.GetImages(),
			confirm:   confirm,
			provision: provision,
		})
		if applyErr != nil {
			return applyErr
		}
		fmt.Fprintf(os.Stdout, "Created %s before building.\n", strings.Join(provision, ", "))
		revision = provisioned.GetRevision()
	}

	var builds map[string]string
	if len(targets) > 0 {
		builds, err = buildServices(ctx, t, archive, targets, os.Stdout)
		if err != nil {
			return err
		}
	}
	applied, err := requestApply(ctx, applyRequest{
		target:   t,
		file:     data,
		revision: revision,
		images:   plan.GetImages(),
		confirm:  confirm,
		builds:   builds,
	})
	if err != nil {
		return err
	}
	if writeErr := writeApplied(os.Stdout, applied); writeErr != nil {
		return writeErr
	}
	return writeReachability(os.Stdout, services)
}

func deployTargets(
	cmd *cobra.Command,
	file *locofile.File,
	services map[string]locofile.Service,
	data []byte,
	names []string,
	environment string,
	quickDeploy bool,
) (map[string]locofile.Service, []buildTarget, []byte, error) {
	if cmd.Flags().Changed("image") && !quickDeploy {
		image, err := cmd.Flags().GetString("image")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read image flag: %w", err)
		}
		data, err = imageOverride(file, services, names, environment, image)
		if err != nil {
			return nil, nil, nil, err
		}
		file, err = locofile.Parse(data)
		if err != nil {
			return nil, nil, nil, err
		}
		services, err = locofile.Resolve(file, environment)
		return services, nil, data, err
	}
	targets, err := buildTargets(services, names)
	return services, targets, data, err
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
	dockerfiles := make([]string, 0, len(targets))
	for _, target := range targets {
		dockerfiles = append(dockerfiles, path.Join(target.context, target.dockerfile))
	}
	archive, err := sourcepack.Pack(dir, dockerfiles)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", dir, err)
	}
	sourcebuild.PackedArchive(out, archive, dir)
	return archive, nil
}

func buildServices(
	ctx context.Context,
	t cmdutil.Target,
	archive *sourcepack.Archive,
	targets []buildTarget,
	out io.Writer,
) (map[string]string, error) {
	builder := sourcebuild.New(t.Clients, out)
	builds := make(map[string]string, len(targets))
	for _, target := range targets {
		res, lookupErr := t.Service(ctx, target.name)
		if lookupErr != nil {
			return nil, lookupErr
		}
		fmt.Fprintf(out, "Building %s from %s in %s\n", target.name, target.dockerfile, target.context)
		build, buildErr := builder.Build(ctx, sourcebuild.Input{
			ResourceID:  res.GetId(),
			WorkspaceID: t.WorkspaceID,
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
