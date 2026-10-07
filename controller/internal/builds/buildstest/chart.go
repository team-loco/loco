package buildstest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"

	"sigs.k8s.io/yaml"
)

var (
	errNoCaller = errors.New("cannot locate the buildstest package")
	errNoBuilds = errors.New("the chart values have no builds block")
)

var chartOnlyKeys = []string{"enabled", "controller", "podSecurity", "agentServiceAccount"}

func ChartValuesPath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errNoCaller
	}
	dir := filepath.Dir(file)
	root := filepath.Join(dir, "..", "..", "..", "..")
	return filepath.Join(root, "charts", "loco-operator", "values.yaml"), nil
}

func ChartBuilds() (map[string]any, error) {
	path, err := ChartValuesPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read chart values: %w", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("parse chart values: %w", err)
	}
	builds, ok := values["builds"].(map[string]any)
	if !ok {
		return nil, errNoBuilds
	}
	for _, key := range chartOnlyKeys {
		delete(builds, key)
	}
	return builds, nil
}

func ChartConfigJSON(overrides map[string]any) (string, error) {
	builds, err := ChartBuilds()
	if err != nil {
		return "", err
	}
	maps.Copy(builds, overrides)
	encoded, err := json.Marshal(builds)
	if err != nil {
		return "", fmt.Errorf("encode build config: %w", err)
	}
	return string(encoded), nil
}
