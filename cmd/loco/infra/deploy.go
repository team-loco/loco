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

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/resource"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/sourcepack"
)

var (
	errServiceNotInFile = errors.New("the file does not run the service in this environment")
	errServiceHasImage  = errors.New("the service runs a public image and is not built")
)

type buildTarget struct {
	name       string
	dockerfile string
	context    string
}

// BuildDeployCmd creates the "deploy" command, which builds the source services of loco.yaml
// and applies the file with the builds.
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
Image services are applied without a build. Without service names, every source service the
file runs in the environment is built.

The plan is shown before anything changes. Destructive operations need
--confirm-destructive and imports need --confirm-import. Without a terminal, --yes
replaces the prompt.

Examples:
  loco deploy
  loco deploy --env staging --yes
  loco deploy web worker --file deploy/loco.yaml`,
		RunE: runDeploy,
	}
	addTargetFlags(cmd)
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
	targets, err := buildTargets(services, names)
	if err != nil {
		return err
	}

	plan, err := requestPlan(ctx, t, data)
	if err != nil {
		return err
	}
	if len(plan.GetErrors()) > 0 {
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
	token := strings.TrimPrefix(t.authHeader, "Bearer ")
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
	req := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{NameKey: nameKey},
	})
	req.Header().Set("Authorization", t.authHeader)
	resp, err := t.resourceClient().GetResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get service %s: %w", name, err)
	}
	return resp.Msg.GetResource(), nil
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
