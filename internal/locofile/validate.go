package locofile

import (
	"errors"
	"math"
	"regexp"
	"slices"
)

const (
	MaxServiceNameLength = 63

	// Failure policy modes and bounds; k8sapi RestartPolicySpec holds the same, update both together.
	RestartOnFailure = "on-failure"
	RestartAlways    = "always"
	RestartNever     = "never"
	MinRestarts      = 1
	MaxRestarts      = 20
	MinDeployTimeout = 60
	MaxDeployTimeout = 3600
)

var dnsLabelPattern = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// validate checks the file's structure: required keys, name shapes and the merged service for
// every environment the file names. Limits on resources belong to the API.
func validate(file *File) error {
	var errs []error
	if file.Version != Version {
		errs = append(errs, &FieldError{Path: keyVersion, Err: ErrUnsupportedVersion})
	}
	switch {
	case file.Partial == "":
		errs = append(errs, &FieldError{Path: keyPartial, Err: ErrPartialRequired})
	case !IsDNSLabel(file.Partial):
		errs = append(errs, &FieldError{Path: keyPartial, Err: ErrInvalidName})
	}
	if file.Services == nil {
		errs = append(errs, &FieldError{Path: keyServices, Err: ErrNoServices})
	}

	for _, name := range sortedKeys(file.Services) {
		service := file.Services[name]
		if !IsDNSLabel(name) {
			errs = append(errs, &FieldError{Path: servicePath(name), Err: ErrInvalidName})
		}
		errs = append(errs, validateService(servicePath(name), service)...)
		for _, env := range sortedKeys(service.Environments) {
			override := service.Environments[env]
			errs = append(errs, validateOverride(servicePath(name, keyEnvironments, env), override)...)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	for _, env := range environmentNames(file) {
		if _, err := Resolve(file, env); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateService(path string, service Service) []error {
	errs := validateRuntimeIDs(path, service.RunAsUser, service.RunAsGroup)
	errs = append(errs, validateDeploy(path, service.Deploy)...)
	if service.Image != "" && (service.Dockerfile != "" || service.Context != "") {
		errs = append(errs, &FieldError{Path: path, Err: ErrImageWithBuild})
	}
	if service.Port == nil {
		if service.Routing != nil {
			errs = append(errs, &FieldError{Path: path + ".routing", Err: ErrPortRequired})
		}
		if len(service.Domains) > 0 {
			errs = append(errs, &FieldError{Path: path + ".domains", Err: ErrPortRequired})
		}
		if service.Health != nil {
			errs = append(errs, &FieldError{Path: path + ".health", Err: ErrPortRequired})
		}
	}
	if len(service.Regions) == 0 {
		errs = append(errs, &FieldError{Path: path + ".regions", Err: ErrNoRegions})
	}
	for _, name := range sortedKeys(service.Regions) {
		region := service.Regions[name]
		regionPath := path + ".regions." + name
		if region.CPU == "" || region.Memory == "" || region.Replicas == nil ||
			region.Replicas.Min == nil || region.Replicas.Max == nil {
			errs = append(errs, &FieldError{Path: regionPath, Err: ErrRegionIncomplete})
		}
		errs = append(errs, validateAutoscaling(regionPath, region.Autoscaling)...)
	}
	return errs
}

func validateOverride(path string, override Override) []error {
	errs := validateRuntimeIDs(path, override.RunAsUser, override.RunAsGroup)
	for _, name := range sortedKeys(override.Regions) {
		region := override.Regions[name]
		errs = append(errs, validateAutoscaling(path+".regions."+name, region.Autoscaling)...)
	}
	return errs
}

func validateDeploy(path string, deploy *Deploy) []error {
	if deploy == nil {
		return nil
	}
	var errs []error
	switch deploy.Restart {
	case "", RestartOnFailure:
		if deploy.MaxRestarts != nil && (*deploy.MaxRestarts < MinRestarts || *deploy.MaxRestarts > MaxRestarts) {
			errs = append(errs, &FieldError{Path: path + ".deploy.maxRestarts", Err: ErrMaxRestarts})
		}
	case RestartAlways, RestartNever:
		if deploy.MaxRestarts != nil {
			errs = append(errs, &FieldError{Path: path + ".deploy.maxRestarts", Err: ErrMaxRestartsMode})
		}
	default:
		errs = append(errs, &FieldError{Path: path + ".deploy.restart", Err: ErrRestart})
	}
	if deploy.Timeout != nil && (*deploy.Timeout < MinDeployTimeout || *deploy.Timeout > MaxDeployTimeout) {
		errs = append(errs, &FieldError{Path: path + ".deploy.timeout", Err: ErrDeployTimeout})
	}
	return errs
}

func validateAutoscaling(path string, autoscaling *Autoscaling) []error {
	if autoscaling == nil {
		return nil
	}
	if (autoscaling.CPUTarget == nil) == (autoscaling.MemoryTarget == nil) {
		return []error{&FieldError{Path: path + ".autoscaling", Err: ErrAutoscalingTarget}}
	}
	return nil
}

// IsDNSLabel reports whether name can name a partial or a service: a DNS label that starts with a letter.
func IsDNSLabel(name string) bool {
	return len(name) <= MaxServiceNameLength && dnsLabelPattern.MatchString(name)
}

func environmentNames(file *File) []string {
	names := map[string]struct{}{}
	for _, service := range file.Services {
		for env := range service.Environments {
			names[env] = struct{}{}
		}
	}
	return sortedKeys(names)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func validateRuntimeIDs(path string, user, group *int64) []error {
	var errs []error
	for field, id := range map[string]*int64{"runAsUser": user, "runAsGroup": group} {
		if id != nil && (*id <= 0 || *id > math.MaxInt32) {
			errs = append(errs, &FieldError{Path: path + "." + field, Err: ErrRuntimeID})
		}
	}
	return errs
}
