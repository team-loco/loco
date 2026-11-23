// Package configplan diffs the services a loco.yaml file declares for one environment against
// the services that environment runs, and returns the operations an apply would perform.
package configplan

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/team-loco/loco/api/pkg/appdomain"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/pkg/varref"
	"github.com/team-loco/loco/internal/locofile"
)

// Kind is what an operation does to a service.
type Kind int

const (
	KindCreate Kind = iota + 1
	KindUpdate
	KindDelete
	KindImport
)

// Service is a service as the environment runs it. Partial is empty when no file owns it.
// Built is true when a succeeded build exists for it, RunningRegions are the regions where it
// has an active deployment in the environment, and Elsewhere is true when another environment
// of the workspace has one. A service the file dropped is deleted only from the environment
// the plan targets; one that has nothing in this environment but runs elsewhere needs no
// operation here.
type Service struct {
	Name           string
	Partial        string
	Built          bool
	RunningRegions []string
	Elsewhere      bool
	State          State
}

// Running reports whether the service has an active deployment in the environment.
func (s Service) Running() bool {
	return len(s.RunningRegions) > 0
}

// ImageResult is the outcome of resolving one image reference from the file to a digest.
type ImageResult struct {
	Pinned string
	Err    error
}

// Input is everything the planner needs. Services are the file's services resolved for the
// environment, Disabled the file's services the environment turns off, FileEnvironments the
// environment names the file mentions, Environments the
// names that exist in the workspace, Clusters the cluster a deployment to each region lands on,
// References the environment's secrets and the workspace's resources the variables refer to,
// SecretSizes the value size in bytes of each secret, MaxSecretBytes the largest total of name
// and value bytes the secrets of one placement may carry, and Images a resolution for every
// image reference the services use.
type Input struct {
	Partial          string
	Services         map[string]locofile.Service
	Disabled         []string
	FileEnvironments []string
	Environments     []string
	Clusters         []appdomain.Cluster
	References       varref.Environment
	SecretSizes      map[string]int64
	MaxSecretBytes   int64
	Live             []Service
	Defaults         servicedefaults.Defaults
	Images           map[string]ImageResult
}

// Change is one field an apply would set.
type Change struct {
	Path   string
	Before string
	After  string
}

// Operation is what an apply would do to one service. Desired is the service as the apply
// writes it, with the API defaults filled in; it is zero for a delete.
type Operation struct {
	Kind        Kind
	Service     string
	Changes     []Change
	Destructive bool
	NeedsDeploy bool
	Desired     State
}

// Error is one reason the plan cannot be applied.
type Error struct {
	Service string
	Path    string
	Err     error
}

// Plan is the operations in service name order, or the errors that stop an apply. A plan with
// errors has no operations. Desired is every service of the file as the apply writes it, with
// the API defaults filled in, including the services the operations leave unchanged, and
// Resolved its variables with their references resolved.
type Plan struct {
	Operations []Operation
	Errors     []Error
	Desired    map[string]State
	Resolved   map[string]varref.Resolved
}

// Compute builds the plan. It returns an error only when the input violates its contract.
func Compute(in Input) (Plan, error) {
	plan := Plan{Desired: make(map[string]State, len(in.Services))}
	plan.Errors = append(plan.Errors, unknownEnvironments(in)...)

	live := make(map[string]Service, len(in.Live))
	for _, service := range in.Live {
		live[service.Name] = service
	}

	for _, name := range slices.Sorted(maps.Keys(in.Services)) {
		service := in.Services[name]
		plan.Errors = append(plan.Errors, checkService(name, service, in)...)

		pinned, err := pinnedImage(service, in.Images)
		if err != nil {
			return Plan{}, err
		}
		desired := fileState(service, pinned, in.Defaults)
		plan.Errors = append(plan.Errors, ValidateState(name, desired, in.Clusters)...)
		plan.Desired[name] = desired

		current, exists := live[name]
		switch {
		case !exists:
			plan.Operations = append(plan.Operations, Operation{
				Kind:        KindCreate,
				Service:     name,
				Changes:     diff(nil, &desired),
				NeedsDeploy: desired.Image == "",
				Desired:     desired,
			})
		case current.Partial == in.Partial:
			op := Update(current, desired)
			if len(op.Changes) != 0 || op.NeedsDeploy {
				plan.Operations = append(plan.Operations, op)
			}
		case current.Partial == "":
			plan.Operations = append(plan.Operations, Operation{
				Kind:        KindImport,
				Service:     name,
				Changes:     withEnabled(current, diff(&current.State, &desired)),
				Destructive: stopsRunningRegion(current, desired),
				NeedsDeploy: needsBuild(current, desired),
				Desired:     desired,
			})
		default:
			owned := fmt.Errorf("%w: %s", ErrOwnedByOtherPartial, current.Partial)
			plan.Errors = append(plan.Errors, Error{Service: name, Err: owned})
		}
	}

	for _, name := range in.Disabled {
		current, exists := live[name]
		if !exists || !current.Running() {
			continue
		}
		if current.Partial != in.Partial && current.Partial != "" {
			owned := fmt.Errorf("%w: %s", ErrOwnedByOtherPartial, current.Partial)
			plan.Errors = append(plan.Errors, Error{Service: name, Err: owned})
			continue
		}
		kind := KindUpdate
		if current.Partial == "" {
			kind = KindImport
		}
		plan.Operations = append(plan.Operations, Operation{
			Kind:        kind,
			Service:     name,
			Changes:     []Change{disabledChange},
			Destructive: true,
		})
	}

	for _, name := range slices.Sorted(maps.Keys(live)) {
		service := live[name]
		if service.Partial != in.Partial {
			continue
		}
		if _, inFile := in.Services[name]; inFile {
			continue
		}
		if slices.Contains(in.Disabled, name) {
			continue
		}
		if service.Elsewhere && !service.Running() && len(service.State.Domains) == 0 {
			continue
		}
		plan.Operations = append(plan.Operations, Operation{Kind: KindDelete, Service: name, Destructive: true})
	}
	slices.SortFunc(plan.Operations, func(a, b Operation) int {
		return strings.Compare(a.Service, b.Service)
	})
	plan.Errors = append(plan.Errors, resolveReferences(in, &plan)...)

	if len(plan.Errors) > 0 {
		plan.Operations = nil
		plan.Desired = nil
		plan.Resolved = nil
	}
	return plan, nil
}

// resolveReferences resolves the variables of every desired service against the environment,
// with the file's services in place of what the environment runs.
func resolveReferences(in Input, plan *Plan) []Error {
	env := varref.Environment{Secrets: in.References.Secrets, Resources: maps.Clone(in.References.Resources)}
	if env.Resources == nil {
		env.Resources = make(map[string]varref.Target, len(plan.Desired))
	}
	for name, desired := range plan.Desired {
		env.Resources[name] = referenceTarget(desired)
	}
	plan.Resolved = make(map[string]varref.Resolved, len(plan.Desired))
	var errs []Error
	for _, name := range slices.Sorted(maps.Keys(plan.Desired)) {
		desired := plan.Desired[name]
		resolved, err := env.Resolve(name, referenceTarget(desired), desired.Env)
		if err != nil {
			for _, variable := range varref.VariableErrors(err) {
				errs = append(errs, Error{Service: name, Path: pathEnv + "." + variable.Name, Err: variable})
			}
			continue
		}
		if total := varref.SecretBytes(resolved.Secret, in.SecretSizes); total > in.MaxSecretBytes {
			tooLarge := ErrSecretsTooLarge.Detail("%d bytes over a limit of %d", total, in.MaxSecretBytes)
			errs = append(errs, Error{Service: name, Path: pathEnv, Err: tooLarge})
			continue
		}
		plan.Resolved[name] = resolved
	}
	return errs
}

func referenceTarget(state State) varref.Target {
	target := varref.Target{Env: state.Env}
	if state.Port != nil {
		target.Port = *state.Port
	}
	// An apply makes the first domain the primary one.
	if len(state.Domains) > 0 {
		target.PublicHost = state.Domains[0]
	}
	return target
}

func unknownEnvironments(in Input) []Error {
	var errs []Error
	for _, name := range slices.Sorted(slices.Values(in.FileEnvironments)) {
		if slices.Contains(in.Environments, name) {
			continue
		}
		unknown := fmt.Errorf("%w: %s", ErrUnknownEnvironment, name)
		errs = append(errs, Error{Path: "environments." + name, Err: unknown})
	}
	return errs
}

func checkService(name string, service locofile.Service, in Input) []Error {
	var errs []Error
	if service.Image != "" {
		if result, found := in.Images[service.Image]; found && result.Err != nil {
			unresolved := fmt.Errorf("%w: %s: %w", ErrImageUnresolved, service.Image, result.Err)
			errs = append(errs, Error{Service: name, Path: pathImage, Err: unresolved})
		}
	}
	return errs
}

func withEnabled(current Service, changes []Change) []Change {
	if current.Running() {
		return changes
	}
	changes = append(changes, enabledChange)
	slices.SortFunc(changes, func(a, b Change) int {
		return strings.Compare(a.Path, b.Path)
	})
	return changes
}

// stopsRunningRegion reports whether the desired regions leave out a region where the service
// runs, which stops that region's deployment.
func stopsRunningRegion(current Service, desired State) bool {
	for _, region := range current.RunningRegions {
		if _, kept := desired.Regions[region]; !kept {
			return true
		}
	}
	return false
}

func needsBuild(current Service, desired State) bool {
	if desired.Image != "" {
		return false
	}
	if !current.Built {
		return true
	}
	return current.State.Dockerfile != desired.Dockerfile || current.State.Context != desired.Context
}

func pinnedImage(service locofile.Service, images map[string]ImageResult) (string, error) {
	if service.Image == "" {
		return "", nil
	}
	result, found := images[service.Image]
	if !found {
		return "", fmt.Errorf("%w: %s", ErrImageNotResolved, service.Image)
	}
	return result.Pinned, nil
}

func Update(current Service, desired State) Operation {
	return Operation{
		Kind:        KindUpdate,
		Service:     current.Name,
		Changes:     withEnabled(current, diff(&current.State, &desired)),
		Destructive: stopsRunningRegion(current, desired),
		NeedsDeploy: needsBuild(current, desired),
		Desired:     desired,
	}
}

func ValidateState(name string, desired State, clusters []appdomain.Cluster) []Error {
	errs := validateDesired(name, desired)
	var targets []appdomain.Cluster
	for _, region := range slices.Sorted(maps.Keys(desired.Regions)) {
		if err := validateRegion(region, desired.Regions[region], clusters); err != nil {
			errs = append(errs, Error{Service: name, Path: pathRegions + "." + region, Err: err})
		}
		for _, cluster := range clusters {
			if cluster.Region == region {
				targets = append(targets, cluster)
				break
			}
		}
	}
	for _, domain := range desired.Domains {
		if _, _, err := appdomain.Check(domain, clusters, targets); err != nil {
			errs = append(errs, Error{Service: name, Path: pathDomains, Err: err})
		}
	}
	return errs
}
