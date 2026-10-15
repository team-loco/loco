package locofile

import (
	"encoding/json/v2"
	"fmt"
	"maps"
)

func OverrideImage(file *File, serviceName, environment, image string) ([]byte, error) {
	services, err := rawServices(file)
	if err != nil {
		return nil, err
	}
	fields, found := services[serviceName]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, serviceName)
	}
	service := maps.Clone(fields)
	environments, hasEnvironments := service[keyEnvironments].(map[string]any)
	if hasEnvironments {
		environments = maps.Clone(environments)
	} else {
		environments = map[string]any{}
	}
	override, hasOverride := environments[environment].(map[string]any)
	if hasOverride {
		override = maps.Clone(override)
	} else {
		override = map[string]any{}
	}
	override["image"] = image
	override["dockerfile"] = nil
	override["context"] = nil
	environments[environment] = override
	service[keyEnvironments] = environments
	services[serviceName] = service
	document := map[string]any{keyVersion: file.Version, keyPartial: file.Partial, keyServices: services}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode image override: %w", err)
	}
	return encoded, nil
}
