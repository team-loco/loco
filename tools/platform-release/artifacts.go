package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	buildsName      = "builds"
	gatewayName     = "gateway"
	fieldVersion    = "version"
	fieldCoreCommit = "coreCommit"
	platformName    = "platform"
)

const crdVersions = "tools/platform-release/crds.yaml"
const gatewayChart = "gateway-helm"

// upstreamCharts are the charts the bundle mirrors from their own repositories, each pinned by a
// file holding its repository, chart and version.
var upstreamCharts = []struct{ name, path, repository string }{
	{"cert-manager-chart", "charts/cert-manager.yaml", "charts/cert-manager"},
	{cloudNativePGChart, "charts/cloudnative-pg.yaml", "charts/cloudnative-pg"},
}

var crdSources = []struct{ name, path string }{
	{"applications", "controller/config/crd/bases/infra.loco.io_applications.yaml"},
	{buildsName, "controller/config/crd/bases/infra.loco.io_builds.yaml"},
	{gatewayName, "charts/loco-worker/Chart.yaml"},
}

type artifact struct {
	name, version string
	data          []byte
	config        any
	mediaType     string
}

func (t *tool) stringValue(manifestPath, key, ref string) (string, error) {
	value, err := t.value(manifestPath, key, ref)
	if err != nil {
		return "", err
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s %s must be a string", manifestPath, key)
	}
	return result, nil
}

func (t *tool) versions(ref string) (map[string]string, error) {
	result := map[string]string{}
	for _, name := range charts {
		version, err := t.stringValue("charts/"+name+"/Chart.yaml", fieldVersion, ref)
		if err != nil {
			return nil, err
		}
		result[name] = version
	}
	for _, entry := range crdSources {
		version, err := t.stringValue(crdVersions, entry.name, ref)
		if err != nil {
			return nil, err
		}
		result["loco-"+entry.name+"-crd"] = version
	}
	for _, version := range result {
		if checkErr := validateVersion(version); checkErr != nil {
			return nil, checkErr
		}
	}
	for _, upstream := range upstreamCharts {
		version, err := t.stringValue(upstream.path, fieldVersion, ref)
		if err != nil {
			return nil, err
		}
		result[upstream.name] = version
	}
	gateway, err := t.gateway(ref)
	if err != nil {
		return nil, err
	}
	gatewayVersion, ok := gateway[fieldVersion].(string)
	if !ok {
		return nil, errors.New("gateway dependency version must be a string")
	}
	result["gateway-chart"] = gatewayVersion
	return result, nil
}

func repositories(repository string) map[string]string {
	result := map[string]string{}
	for _, name := range charts {
		result[name] = repository + "/charts/" + name
	}
	for _, entry := range crdSources {
		result["loco-"+entry.name+"-crd"] = repository + "/crds/" + entry.name
	}
	for _, upstream := range upstreamCharts {
		result[upstream.name] = repository + "/" + upstream.repository
	}
	result["gateway-chart"] = repository + "/charts/gateway-helm"
	result[platformName] = repository + "/platform"
	return result
}

func (t *tool) unbumped(base string) ([]string, error) {
	stale := []string{}
	for _, entry := range crdSources {
		old, err := t.value(crdVersions, entry.name, base)
		if err != nil {
			return nil, err
		}
		if old == nil {
			continue
		}
		a, err := t.crdDocument(entry.name, entry.path, base)
		if err != nil {
			return nil, err
		}
		b, err := t.crdDocument(entry.name, entry.path, "")
		if err != nil {
			return nil, err
		}
		if bytes.Equal(encoded(a), encoded(b)) {
			continue
		}
		previous, ok := old.(string)
		if !ok {
			return nil, errors.New("old CRD version must be a string")
		}
		current, err := t.stringValue(crdVersions, entry.name, "")
		if err != nil {
			return nil, err
		}
		order, err := compareVersions(current, previous)
		if err != nil {
			return nil, err
		}
		if order <= 0 {
			stale = append(stale, "loco-"+entry.name+"-crd")
		}
	}
	return stale, nil
}

func (t *tool) protectedCRD(manifestPath string) ([]byte, error) {
	value, err := t.value(manifestPath, "", "")
	if err != nil {
		return nil, err
	}
	item, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("CRD must be an object")
	}
	metadata, ok := item[fieldMetadata].(map[string]any)
	if !ok {
		return nil, errors.New("CRD must have metadata")
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		annotations = map[string]any{}
		metadata["annotations"] = annotations
	}
	annotations["kustomize.toolkit.fluxcd.io/prune"] = "disabled"
	return archive(releaseFiles{"crd.yaml": encoded(item), "kustomization.yaml": kustomization([]string{"crd.yaml"})})
}

func gatewayCRDs(data []byte) ([]byte, error) {
	entries, err := archiveFiles(data)
	if err != nil {
		return nil, err
	}
	output := releaseFiles{}
	for name, data := range entries {
		if strings.Contains(name, "/crds/crds/") &&
			(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			output[path.Base(name)] = data
		}
	}
	if len(output) == 0 {
		return nil, errors.New("gateway dependency contains no CRDs")
	}
	names := make([]string, 0, len(output))
	for name := range output {
		names = append(names, name)
	}
	slices.Sort(names)
	output["kustomization.yaml"] = encoded(
		object{
			fieldAPIVersion:     "kustomize.config.k8s.io/v1beta1",
			fieldKind:           kindKustomization,
			"resources":         names,
			"commonAnnotations": object{"kustomize.toolkit.fluxcd.io/prune": "disabled"},
		},
	)
	return archive(output)
}

func (t *tool) packagedArtifacts(directory, commit string) ([]artifact, error) {
	pinned, err := t.versions("")
	if err != nil {
		return nil, err
	}
	if _, checkErr := t.run("mise", "run", "helm:deps"); checkErr != nil {
		return nil, checkErr
	}
	result := []artifact{}
	for _, name := range charts {
		if _, checkErr := t.run(
			"helm",
			"package",
			filepath.Join(t.root, "charts", name),
			"--destination",
			directory,
		); checkErr != nil {
			return nil, checkErr
		}
		config, configErr := t.value("charts/"+name+"/Chart.yaml", "", "")
		if configErr != nil {
			return nil, configErr
		}
		data, readErr := os.ReadFile(filepath.Join(directory, name+"-"+pinned[name]+".tgz"))
		if readErr != nil {
			return nil, readErr
		}
		result = append(result, artifact{name, pinned[name], data, config, helmLayerType})
	}
	for _, upstream := range upstreamCharts {
		mirrored, pullErr := t.pullUpstream(directory, upstream.name, upstream.path, pinned[upstream.name])
		if pullErr != nil {
			return nil, pullErr
		}
		result = append(result, mirrored)
	}
	data, err := os.ReadFile(
		filepath.Join(t.root, "charts", "loco-worker", "charts", "gateway-helm-"+pinned["gateway-chart"]+".tgz"),
	)
	if err != nil {
		return nil, err
	}
	result = append(
		result,
		artifact{
			"gateway-chart",
			pinned["gateway-chart"],
			data,
			object{fieldName: gatewayChart, fieldVersion: pinned["gateway-chart"]},
			helmLayerType,
		},
	)
	gatewayData := data
	for _, entry := range crdSources {
		var data []byte
		if entry.name == gatewayName {
			data, err = gatewayCRDs(gatewayData)
		} else {
			data, err = t.protectedCRD(entry.path)
		}
		if err != nil {
			return nil, err
		}
		name := "loco-" + entry.name + "-crd"
		result = append(
			result,
			artifact{name, pinned[name], data, object{fieldVersion: pinned[name], fieldCoreCommit: commit}, layerType},
		)
	}
	return result, nil
}

func (t *tool) gateway(ref string) (object, error) {
	dependencies, err := t.value("charts/loco-worker/Chart.yaml", "dependencies", ref)
	if err != nil {
		return nil, err
	}
	entries, ok := dependencies.([]any)
	if !ok {
		return nil, errors.New("worker chart dependencies must be a list")
	}
	for _, value := range entries {
		dependency, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("chart dependency must be an object")
		}
		if dependency[fieldName] == gatewayChart {
			return dependency, nil
		}
	}
	return nil, errors.New("worker chart has no gateway-helm dependency")
}

func (t *tool) crdDocument(name, manifestPath, ref string) (any, error) {
	if name == gatewayName {
		return t.gateway(ref)
	}
	return t.value(manifestPath, "", ref)
}

// pullUpstream downloads the chart the settings file pins, as the artifact name.
func (t *tool) pullUpstream(directory, name, settingsPath, version string) (artifact, error) {
	value, err := t.value(settingsPath, "", "")
	if err != nil {
		return artifact{}, err
	}
	settings, ok := value.(map[string]any)
	if !ok {
		return artifact{}, fmt.Errorf("%s must be an object", settingsPath)
	}
	chart, ok := settings["chart"].(string)
	if !ok {
		return artifact{}, fmt.Errorf("%s chart must be a string", settingsPath)
	}
	repository, ok := settings["repository"].(string)
	if !ok {
		return artifact{}, fmt.Errorf("%s repository must be a string", settingsPath)
	}
	if _, err = t.run(
		"helm", "pull", chart, "--repo", repository, "--version", version, "--destination", directory,
	); err != nil {
		return artifact{}, err
	}
	data, err := os.ReadFile(filepath.Join(directory, chart+"-"+version+".tgz"))
	if err != nil {
		return artifact{}, err
	}
	return artifact{
		name:      name,
		version:   version,
		data:      data,
		config:    object{fieldName: chart, fieldVersion: version},
		mediaType: helmLayerType,
	}, nil
}
