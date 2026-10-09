// Package configplan diffs the services a loco.yaml file declares for one environment against
// the services that environment runs, and returns the operations an apply would perform.
package configplan

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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
// Built is true when a succeeded build exists for it.
type Service struct {
	Name    string
	Partial string
	Built   bool
	State   State
}

// ImageResult is the outcome of resolving one image reference from the file to a digest.
type ImageResult struct {
	Pinned string
	Err    error
}

// Input is everything the planner needs. Services are the file's services resolved for the
// environment, FileEnvironments the environment names the file mentions, Environments the
// names that exist in the workspace, Regions the regions with an active cluster, Secrets the
// secret names set in the environment, and Images a resolution for every image reference
// the services use.
type Input struct {
	Partial          string
	Services         map[string]locofile.Service
	FileEnvironments []string
	Environments     []string
	Regions          []string
	Secrets          []string
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
// errors has no operations.
type Plan struct {
	Operations []Operation
	Errors     []Error
}

// Compute builds the plan. It returns an error only when the input violates its contract.
func Compute(in Input) (Plan, error) {
	var plan Plan
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
			changes := diff(&current.State, &desired)
			needsDeploy := needsBuild(current, desired)
			if len(changes) == 0 && !needsDeploy {
				continue
			}
			plan.Operations = append(plan.Operations, Operation{
				Kind:        KindUpdate,
				Service:     name,
				Changes:     changes,
				NeedsDeploy: needsDeploy,
				Desired:     desired,
			})
		case current.Partial == "":
			plan.Operations = append(plan.Operations, Operation{
				Kind:        KindImport,
				Service:     name,
				Changes:     diff(&current.State, &desired),
				NeedsDeploy: needsBuild(current, desired),
				Desired:     desired,
			})
		default:
			owned := fmt.Errorf("%w: %s", ErrOwnedByOtherPartial, current.Partial)
			plan.Errors = append(plan.Errors, Error{Service: name, Err: owned})
		}
	}

	for _, name := range slices.Sorted(maps.Keys(live)) {
		service := live[name]
		if service.Partial != in.Partial {
			continue
		}
		if _, inFile := in.Services[name]; inFile {
			continue
		}
		plan.Operations = append(plan.Operations, Operation{Kind: KindDelete, Service: name, Destructive: true})
	}
	slices.SortFunc(plan.Operations, func(a, b Operation) int {
		return strings.Compare(a.Service, b.Service)
	})

	if len(plan.Errors) > 0 {
		plan.Operations = nil
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
	for _, region := range slices.Sorted(maps.Keys(service.Regions)) {
		if slices.Contains(in.Regions, region) {
			continue
		}
		unknown := fmt.Errorf("%w: %s", ErrUnknownRegion, region)
		errs = append(errs, Error{Service: name, Path: pathRegions + "." + region, Err: unknown})
	}
	for _, secret := range slices.Sorted(slices.Values(service.Secrets)) {
		if slices.Contains(in.Secrets, secret) {
			continue
		}
		missing := fmt.Errorf("%w: %s", ErrMissingSecret, secret)
		errs = append(errs, Error{Service: name, Path: pathSecrets, Err: missing})
	}
	if service.Image != "" {
		if result, found := in.Images[service.Image]; found && result.Err != nil {
			unresolved := fmt.Errorf("%w: %s: %w", ErrImageUnresolved, service.Image, result.Err)
			errs = append(errs, Error{Service: name, Path: pathImage, Err: unresolved})
		}
	}
	return errs
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
