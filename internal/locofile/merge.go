package locofile

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
)

// Merge applies an environment override to a service: objects merge recursively, lists and
// scalars replace whole, and a null removes the key. Neither input is modified.
func Merge(service, override map[string]any) map[string]any {
	merged := maps.Clone(service)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range override {
		if value == nil {
			delete(merged, key)
			continue
		}
		overrideObject, overrideIsObject := value.(map[string]any)
		baseObject, baseIsObject := merged[key].(map[string]any)
		if overrideIsObject && baseIsObject {
			merged[key] = Merge(baseObject, overrideObject)
			continue
		}
		merged[key] = value
	}
	return merged
}

// Resolve returns every service as it applies to env, with that environment's override merged
// in and services the override disables left out. Each merged service is validated.
func Resolve(file *File, env string) (map[string]Service, error) {
	rawServices, err := rawServices(file)
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]Service, len(rawServices))
	for _, name := range sortedKeys(rawServices) {
		base := maps.Clone(rawServices[name])
		environments, hasEnvironments := base[keyEnvironments].(map[string]any)
		delete(base, keyEnvironments)

		override, hasOverride := environments[env].(map[string]any)
		if hasEnvironments && hasOverride {
			if enabled, isBool := override[keyEnabled].(bool); isBool && !enabled {
				continue
			}
			override = maps.Clone(override)
			delete(override, keyEnabled)
			base = Merge(base, override)
		}

		service, err := decodeService(base)
		if err != nil {
			return nil, &FieldError{Path: servicePath(name, keyEnvironments, env), Err: err}
		}
		path := fmt.Sprintf("%s (environment %s)", servicePath(name), env)
		if errs := validateService(path, service); len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		resolved[name] = service
	}
	return resolved, nil
}

func rawServices(file *File) (map[string]map[string]any, error) {
	raw := file.raw
	if raw == nil {
		encoded, err := json.Marshal(file)
		if err != nil {
			return nil, fmt.Errorf("encode file: %w", err)
		}
		if err := json.Unmarshal(encoded, &raw); err != nil {
			return nil, fmt.Errorf("decode file: %w", err)
		}
	}
	services, hasServices := raw[keyServices].(map[string]any)
	if !hasServices {
		return nil, &FieldError{Path: keyServices, Err: ErrNoServices}
	}
	result := make(map[string]map[string]any, len(services))
	for name, value := range services {
		fields, ok := value.(map[string]any)
		if !ok {
			return nil, &FieldError{Path: servicePath(name), Err: ErrNotAnObject}
		}
		result[name] = fields
	}
	return result, nil
}

func decodeService(fields map[string]any) (Service, error) {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return Service{}, fmt.Errorf("encode service: %w", err)
	}
	var service Service
	if err := json.Unmarshal(encoded, &service, json.RejectUnknownMembers(true)); err != nil {
		return Service{}, newDecodeError(err)
	}
	return service, nil
}
