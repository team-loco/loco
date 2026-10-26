package infra

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/cmd/loco/resource"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/sourcepack"
	"github.com/team-loco/loco/internal/ui"
	"sigs.k8s.io/yaml"
)

const (
	domainSuffixLength = 4
	domainAlphabetSize = 26
	maxDNSLabelLength  = 63
)

var (
	errImageTarget = errors.New(
		"--image requires selecting exactly one service; omit the name only for a single-service environment",
	)
	errEmptyImage          = errors.New("image reference must not be empty")
	errServiceNotInFile    = errors.New("the file does not run the service in this environment")
	errServiceHasImage     = errors.New("the service runs a public image and is not built")
	errQuickDeployFlags    = errors.New("deployment flags for a new service require omitting loco.yaml")
	errNameConflict        = errors.New("set the service name with either an argument or --name")
	errMultipleNames       = errors.New("without loco.yaml, deploy one service at a time")
	errDomainConflict      = errors.New("--domain and --private cannot be used together")
	errUnsafeDirectoryName = errors.New("directory name has no usable letters or digits; set --name")
	errDomainUnavailable   = errors.New("generated public hostname is unavailable; set --domain")
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
		file, data, err = buildQuickDeploy(cmd, names, t, confirm.yes)
		if err != nil {
			return err
		}
		filePath = "current directory"
		names = nil
	}
	services, err := locofile.Resolve(file, t.environmentName)
	if err != nil {
		return fmt.Errorf("%s does not resolve for environment %s: %w", filePath, t.environmentName, err)
	}
	services, targets, data, err := deployTargets(cmd, file, services, data, names, t.environmentName, quickDeploy)
	if err != nil {
		return err
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

func loadDeployFile(cmd *cobra.Command, filePath string) (*locofile.File, []byte, bool, error) {
	file, data, err := loadLocoFile(filePath)
	quickDeploy := errors.Is(err, os.ErrNotExist) && !cmd.Flags().Changed("file")
	if err != nil && !quickDeploy {
		return nil, nil, false, err
	}
	if !quickDeploy && changedQuickDeployFlag(cmd) {
		return nil, nil, false, errQuickDeployFlags
	}
	return file, data, quickDeploy, nil
}

func changedQuickDeployFlag(cmd *cobra.Command) bool {
	for _, name := range []string{"name", "port", "region", "domain", "private", "dockerfile", "context"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func buildQuickDeploy(cmd *cobra.Command, names []string, t target, yes bool) (*locofile.File, []byte, error) {
	name, err := quickServiceName(cmd, names, yes)
	if err != nil {
		return nil, nil, err
	}
	config, err := fetchConfig(cmd)
	if err != nil {
		return nil, nil, err
	}
	defaults := config.GetServiceDefaults()
	port, err := cmd.Flags().GetInt32("port")
	if err != nil {
		return nil, nil, err
	}
	if !cmd.Flags().Changed("port") {
		port = defaults.GetRouting().GetPort()
	}
	region, err := cmd.Flags().GetString("region")
	if err != nil {
		return nil, nil, err
	}
	if region == "" {
		ctx := cmd.Context()
		region, err = fetchDefaultRegion(ctx, t)
		if err != nil {
			return nil, nil, err
		}
	}
	domain, err := quickDomain(cmd, t, name, region)
	if err != nil {
		return nil, nil, err
	}
	dockerfile, err := cmd.Flags().GetString("dockerfile")
	if err != nil {
		return nil, nil, err
	}
	contextDir, err := cmd.Flags().GetString("context")
	if err != nil {
		return nil, nil, err
	}
	image, err := cmd.Flags().GetString("image")
	if err != nil {
		return nil, nil, err
	}
	if cmd.Flags().Changed("image") && strings.TrimSpace(image) == "" {
		return nil, nil, errEmptyImage
	}
	minReplicas := defaults.GetMinReplicas()
	maxReplicas := defaults.GetMaxReplicas()
	service := locofile.Service{
		Port:       new(port),
		Dockerfile: dockerfile,
		Context:    contextDir,
		Image:      image,
		Regions: map[string]locofile.Region{region: {
			CPU:      defaults.GetCpu(),
			Memory:   defaults.GetMemory(),
			Replicas: &locofile.Replicas{Min: new(minReplicas), Max: new(maxReplicas)},
		}},
	}
	if domain != "" {
		service.Domains = []string{domain}
	}
	generated := locofile.File{
		Version:  locofile.Version,
		Partial:  name,
		Services: map[string]locofile.Service{name: service},
	}
	data, err := yaml.Marshal(generated)
	if err != nil {
		return nil, nil, fmt.Errorf("render deployment: %w", err)
	}
	file, err := locofile.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("generated deployment: %w", err)
	}
	return file, data, nil
}

func quickServiceName(cmd *cobra.Command, names []string, yes bool) (string, error) {
	if len(names) > 1 {
		return "", errMultipleNames
	}
	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return "", err
	}
	if name != "" && len(names) > 0 {
		return "", errNameConflict
	}
	if len(names) == 1 {
		name = names[0]
	}
	if name == "" && cmdutil.IsInteractive() && !yes {
		name, err = ui.AskForString("Service name (press Enter to use the directory name): ")
		if err != nil {
			return "", fmt.Errorf("read service name: %w", err)
		}
	}
	if name == "" {
		dir, getwdErr := os.Getwd()
		if getwdErr != nil {
			return "", fmt.Errorf("get current directory: %w", getwdErr)
		}
		name, err = safeDirectoryName(filepath.Base(dir))
		if err != nil {
			return "", err
		}
	}
	return name, nil
}

func safeDirectoryName(dir string) (string, error) {
	var b strings.Builder
	for _, char := range strings.ToLower(dir) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			b.WriteRune(char)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
		if b.Len() >= maxDNSLabelLength {
			break
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "", errUnsafeDirectoryName
	}
	return name, nil
}

func quickDomain(cmd *cobra.Command, t target, name, region string) (string, error) {
	domain, err := cmd.Flags().GetString("domain")
	if err != nil {
		return "", err
	}
	private, err := cmd.Flags().GetBool("private")
	if err != nil {
		return "", err
	}
	if private && domain != "" {
		return "", errDomainConflict
	}
	if !private && domain == "" {
		ctx := cmd.Context()
		platformDomain, domainErr := servedDomain(ctx, t, region)
		if domainErr != nil {
			return "", domainErr
		}
		return generatedDomain(ctx, t, name, platformDomain)
	}
	return domain, nil
}

func generatedDomain(ctx context.Context, t target, name, platformDomain string) (string, error) {
	prefix := name
	if len(prefix) > maxDNSLabelLength-domainSuffixLength-1 {
		prefix = strings.TrimSuffix(prefix[:maxDNSLabelLength-domainSuffixLength-1], "-")
	}
	base := prefix + "-"
	res, err := t.lookupResource(ctx, name)
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		return "", err
	}
	if err == nil {
		for _, assigned := range res.GetDomains() {
			domain := assigned.GetDomain()
			if strings.HasPrefix(domain, base) && strings.HasSuffix(domain, "."+platformDomain) {
				suffix := strings.TrimSuffix(strings.TrimPrefix(domain, base), "."+platformDomain)
				if isDomainSuffix(suffix) {
					return domain, nil
				}
			}
		}
	}
	client := rpc.ForHost(t.host, t.token).Domain
	candidate := base + domainSuffix(t.workspaceID, name) + "." + platformDomain
	request := &domainv1.CheckDomainAvailabilityRequest{Domain: candidate}
	response, checkErr := client.CheckDomainAvailability(ctx, request)
	if checkErr != nil {
		return "", fmt.Errorf("check public hostname %s: %w", candidate, checkErr)
	}
	if !response.GetIsAvailable() {
		return "", fmt.Errorf("%w: %s", errDomainUnavailable, candidate)
	}
	return candidate, nil
}

func domainSuffix(workspaceID, name string) string {
	input := workspaceID + "/" + name
	hash := sha256.Sum256([]byte(input))
	value := binary.BigEndian.Uint32(hash[:])
	suffix := [domainSuffixLength]byte{}
	for index := domainSuffixLength - 1; index >= 0; index-- {
		suffix[index] = 'a' + byte(value%domainAlphabetSize)
		value /= domainAlphabetSize
	}
	return string(suffix[:])
}

func isDomainSuffix(suffix string) bool {
	if len(suffix) != domainSuffixLength {
		return false
	}
	for _, char := range suffix {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return true
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
