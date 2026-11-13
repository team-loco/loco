// Package configplan diffs the services a loco.yaml file declares for one environment against
// the services that environment runs, and returns the operations an apply would perform.
package configplan

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/team-loco/loco/api/pkg/platformdomain"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
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
// Secrets the value size in bytes of every secret name each service holds in the environment,
// keyed by service name, MaxSecretBytes the largest total of name and value bytes one placement
// of a service may carry, Provision the services an apply creates without deploying, whose
// missing secrets are not errors when the plan creates them, and Images a resolution for every
// image reference the services use.
type Input struct {
	Partial          string
	Services         map[string]locofile.Service
	Disabled         []string
	FileEnvironments []string
	Environments     []string
	Clusters         []platformdomain.Cluster
	Secrets          map[string]map[string]int64
	MaxSecretBytes   int64
	Provision        []string
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
// the API defaults filled in, including the services the operations leave unchanged.
type Plan struct {
	Operations []Operation
	Errors     []Error
	Desired    map[string]State
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
		_, exists := live[name]
		provisioned := !exists && slices.Contains(in.Provision, name)
		plan.Errors = append(plan.Errors, checkService(name, service, in)...)
		if !provisioned {
			plan.Errors = append(plan.Errors, checkSecrets(name, service, in)...)
		}

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

	if len(plan.Errors) > 0 {
		plan.Operations = nil
		plan.Desired = nil
	}
	return plan, nil
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

func checkSecrets(name string, service locofile.Service, in Input) []Error {
	bindingErrors := CheckSecretBindings(service.Secrets, in.Secrets[name], in.MaxSecretBytes)
	errs := make([]Error, 0, len(bindingErrors))
	for _, err := range bindingErrors {
		errs = append(errs, Error{Service: name, Path: pathSecrets, Err: err})
	}
	return errs
}

// CheckSecretBindings validates selected names against stored plaintext lengths and the placement limit.
func CheckSecretBindings(names []string, held map[string]int64, maxBytes int64) []error {
	var errs []error
	var total int64
	for _, name := range slices.Sorted(slices.Values(names)) {
		valueBytes, found := held[name]
		if !found {
			errs = append(errs, ErrMissingSecret.Detail("%s", name))
			continue
		}
		total += int64(len(name)) + valueBytes
	}
	if total > maxBytes {
		errs = append(errs, ErrSecretsTooLarge.Detail("%d bytes over a limit of %d", total, maxBytes))
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

func ValidateState(name string, desired State, clusters []platformdomain.Cluster) []Error {
	errs := validateDesired(name, desired)
	var targets []platformdomain.Cluster
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
		if _, _, err := platformdomain.Check(domain, clusters, targets); err != nil {
			errs = append(errs, Error{Service: name, Path: pathDomains, Err: err})
		}
	}
	return errs
}
