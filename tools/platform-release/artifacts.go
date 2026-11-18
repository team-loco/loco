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

const crdVersions = "tools/platform-release/crds.yaml"
const certManager = "charts/cert-manager.yaml"
const gatewayExpression = `.dependencies[] | select(.name == "gateway-helm")`

var crdSources = []struct{ name, path, expression string }{
	{"applications", "controller/config/crd/bases/infra.loco.io_applications.yaml", "."},
	{buildsName, "controller/config/crd/bases/infra.loco.io_builds.yaml", "."},
	{gatewayName, "charts/loco-worker/Chart.yaml", gatewayExpression},
}

type artifact struct {
	name, version string
	data          []byte
	config        any
	mediaType     string
}

func (t *tool) stringValue(manifestPath, expression, ref string) (string, error) {
	value, err := t.value(manifestPath, expression, ref)
	if err != nil {
		return "", err
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s %s must be a string", manifestPath, expression)
	}
	return result, nil
}

func (t *tool) versions(ref string) (map[string]string, error) {
	result := map[string]string{}
	for _, name := range charts {
		version, err := t.stringValue("charts/"+name+"/Chart.yaml", ".version", ref)
		if err != nil {
			return nil, err
		}
		result[name] = version
	}
	for _, entry := range crdSources {
		version, err := t.stringValue(crdVersions, "."+entry.name, ref)
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
	cert, err := t.stringValue(certManager, ".version", ref)
	if err != nil {
		return nil, err
	}
	result["cert-manager-chart"] = cert
	gateway, err := t.stringValue("charts/loco-worker/Chart.yaml", gatewayExpression+" | .version", ref)
	if err != nil {
		return nil, err
	}
	result["gateway-chart"] = gateway
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
	result["cert-manager-chart"] = repository + "/charts/cert-manager"
	result["gateway-chart"] = repository + "/charts/gateway-helm"
	result[fieldPlatform] = repository + "/platform"
	return result
}

func (t *tool) unbumped(base string) ([]string, error) {
	stale := []string{}
	for _, entry := range crdSources {
		old, err := t.value(crdVersions, "."+entry.name, base)
		if err != nil {
			return nil, err
		}
		if old == nil {
			continue
		}
		a, err := t.value(entry.path, entry.expression, base)
		if err != nil {
			return nil, err
		}
		b, err := t.value(entry.path, entry.expression, "")
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
		current, err := t.stringValue(crdVersions, "."+entry.name, "")
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
	value, err := t.value(manifestPath, ".", "")
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
		config, configErr := t.value("charts/"+name+"/Chart.yaml", ".", "")
		if configErr != nil {
			return nil, configErr
		}
		data, readErr := os.ReadFile(filepath.Join(directory, name+"-"+pinned[name]+".tgz"))
		if readErr != nil {
			return nil, readErr
		}
		result = append(result, artifact{name, pinned[name], data, config, helmLayerType})
	}
	cert, err := t.value(certManager, ".", "")
	if err != nil {
		return nil, err
	}
	settings, ok := cert.(map[string]any)
	if !ok {
		return nil, errors.New("cert-manager chart settings must be an object")
	}
	chart, ok := settings["chart"].(string)
	if !ok {
		return nil, errors.New("cert-manager chart must be a string")
	}
	repository, ok := settings["repository"].(string)
	if !ok {
		return nil, errors.New("cert-manager repository must be a string")
	}
	if _, checkErr := t.run(
		"helm",
		"pull",
		chart,
		"--repo",
		repository,
		"--version",
		pinned["cert-manager-chart"],
		"--destination",
		directory,
	); checkErr != nil {
		return nil, checkErr
	}
	certFiles, err := filepath.Glob(filepath.Join(directory, "cert-manager-*.tgz"))
	if err != nil {
		return nil, err
	}
	if len(certFiles) != 1 {
		return nil, errors.New("expected one cert-manager chart archive")
	}
	certData, err := os.ReadFile(certFiles[0])
	if err != nil {
		return nil, err
	}
	result = append(
		result,
		artifact{
			"cert-manager-chart",
			pinned["cert-manager-chart"],
			certData,
			object{fieldName: chart, fieldVersion: pinned["cert-manager-chart"]},
			helmLayerType,
		},
	)
	gateways, err := filepath.Glob(filepath.Join(t.root, "charts", "loco-worker", "charts", "gateway-helm-*.tgz"))
	if err != nil {
		return nil, err
	}
	if len(gateways) != 1 {
		return nil, errors.New("expected one gateway dependency archive")
	}
	data, err := os.ReadFile(gateways[0])
	if err != nil {
		return nil, err
	}
	result = append(
		result,
		artifact{
			"gateway-chart",
			pinned["gateway-chart"],
			data,
			object{fieldName: "gateway-helm", fieldVersion: pinned["gateway-chart"]},
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
