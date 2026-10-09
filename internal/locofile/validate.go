package locofile

import (
	"errors"
	"regexp"
	"slices"
)

const (
	dnsLabelMaxLength = 63
)

var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Validate checks the file's structure: required keys, name shapes and the merged service for
// every environment the file names. Limits on resources belong to the API.
func Validate(file *File) error {
	var errs []error
	if file.Version != Version {
		errs = append(errs, &FieldError{Path: keyVersion, Err: ErrUnsupportedVersion})
	}
	switch {
	case file.Partial == "":
		errs = append(errs, &FieldError{Path: keyPartial, Err: ErrPartialRequired})
	case !isDNSLabel(file.Partial):
		errs = append(errs, &FieldError{Path: keyPartial, Err: ErrInvalidName})
	}
	if len(file.Services) == 0 {
		errs = append(errs, &FieldError{Path: keyServices, Err: ErrNoServices})
	}

	for _, name := range sortedKeys(file.Services) {
		service := file.Services[name]
		if !isDNSLabel(name) {
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
	var errs []error
	if service.Image != "" && (service.Dockerfile != "" || service.Context != "") {
		errs = append(errs, &FieldError{Path: path, Err: ErrImageWithBuild})
	}
	if service.Routing != nil && service.Port == nil {
		errs = append(errs, &FieldError{Path: path + ".port", Err: ErrPortRequired})
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
	errs := make([]error, 0, len(override.Regions))
	for _, name := range sortedKeys(override.Regions) {
		region := override.Regions[name]
		errs = append(errs, validateAutoscaling(path+".regions."+name, region.Autoscaling)...)
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

func isDNSLabel(name string) bool {
	return len(name) <= dnsLabelMaxLength && dnsLabelPattern.MatchString(name)
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
