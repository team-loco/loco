package main

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

type object = map[string]any
type releaseFiles = map[string][]byte

var charts = []string{"loco-worker", "loco-control-plane"}

const workerLayer = "worker"

const namespacesLayer = "namespaces"
const imageController = "controller"
const fieldPlatform = "platform"

var images = []string{imageAgent, imageController, "builder", "obs-proxy", imageAPI, "ui"}

var dockerfiles = map[string]string{
	imageAgent:      "agent/Dockerfile",
	imageController: "controller/Dockerfile",
	"builder":       "builder/Dockerfile",
	"obs-proxy":     "observability-proxy/Dockerfile",
	imageAPI:        "api/Dockerfile",
	"ui":            "web/Dockerfile",
}

type releaseManifest struct {
	Version    string            `json:"version"`
	CoreCommit string            `json:"coreCommit"`
	Artifacts  map[string]string `json:"artifacts"`
	Images     map[string]string `json:"images"`
	Bundle     string            `json:"bundle,omitempty"`
}

const dependencyExpression = "dep.metadata.annotations['platform.loco.io/revision'] == " +
	"self.metadata.annotations['platform.loco.io/revision'] && dep.metadata.generation == " +
	"dep.status.observedGeneration && dep.status.conditions.exists(c, c.type == 'Ready' && c.status " +
	"== 'True')"

const crdHealthExpression = "metadata.generation == status.observedGeneration && " +
	"status.conditions.exists(c, c.type == " +
	"'Ready' && c.status == 'True') && status.lastAppliedRevision == " +
	"metadata.annotations['platform.loco.io/artifact-digest']"

func resource(kind, name string, spec object) object {
	versions := map[string]string{
		kindOCIRepository: "source.toolkit.fluxcd.io/v1",
		kindKustomization: "kustomize.toolkit.fluxcd.io/v1",
		"HelmRelease":     "helm.toolkit.fluxcd.io/v2",
	}
	return object{
		fieldAPIVersion: versions[kind],
		fieldKind:       kind,
		fieldMetadata:   object{fieldName: name, fieldNamespace: fluxNamespace},
		"spec":          spec,
	}
}

func source(name, reference, mediaType string) (object, error) {
	repository, pinned, ok := strings.Cut(reference, "@")
	if !ok || !digestPattern.MatchString(pinned) {
		return nil, errors.New("artifact reference must contain a sha256 digest")
	}
	return resource(
		kindOCIRepository,
		name,
		object{
			fieldInterval:   "5m",
			"url":           "oci://" + repository,
			"ref":           object{fieldDigest: pinned},
			"layerSelector": object{fieldMediaType: mediaType, "operation": "copy"},
		},
	), nil
}

func dependency(name string) object {
	return object{fieldName: name, "readyExpr": dependencyExpression}
}

func kustomization(resources []string) []byte {
	return encoded(
		object{
			fieldAPIVersion: "kustomize.config.k8s.io/v1beta1",
			fieldKind:       kindKustomization,
			"resources":     resources,
		},
	)
}

func (t *tool) bundle(manifest releaseManifest) (releaseFiles, error) {
	namespace, err := t.value("manifests/namespaces/loco-system.yaml", "", "")
	if err != nil {
		return nil, err
	}
	ns, ok := namespace.(map[string]any)
	if !ok {
		return nil, errors.New("loco-system namespace manifest must be an object")
	}
	return buildBundle(manifest, ns)
}

func buildBundle(manifest releaseManifest, ns object) (releaseFiles, error) {
	output := releaseFiles{}
	revision := strings.TrimPrefix(
		digest(
			encoded(
				object{fieldVersion: manifest.Version, artifactsName: manifest.Artifacts, "images": manifest.Images},
			),
		),
		"sha256:",
	)
	labels := object{"platform.loco.io/release": manifest.Version}
	annotations := object{"platform.loco.io/revision": revision}
	writeLayer := func(name string, objects []object) {
		paths := make([]string, 0, len(objects))
		for _, item := range objects {
			nameInLayer := documentName(item) + "-" + strings.ToLower(
				documentKind(item),
			) + ".yaml"
			output[path.Join("layers", name, nameInLayer)] = encoded(item)
			paths = append(paths, nameInLayer)
		}
		output[path.Join("layers", name, "kustomization.yaml")] = kustomization(paths)
	}
	writeLayer(namespacesLayer, []object{
		ns,
		{fieldAPIVersion: "v1", fieldKind: "Namespace", fieldMetadata: object{fieldName: certManagerName}},
		{
			fieldAPIVersion: "v1",
			fieldKind:       "ServiceAccount",
			fieldMetadata:   object{fieldName: reconcilerAccount, fieldNamespace: fluxNamespace},
		},
		{
			fieldAPIVersion: "rbac.authorization.k8s.io/v1",
			fieldKind:       "ClusterRoleBinding",
			fieldMetadata:   object{fieldName: "loco-platform-reconciler"},
			"roleRef": object{
				"apiGroup": "rbac.authorization.k8s.io",
				fieldKind:  "ClusterRole",
				fieldName:  "cluster-admin",
			},
			"subjects": []object{
				{fieldKind: "ServiceAccount", fieldName: reconcilerAccount, fieldNamespace: fluxNamespace},
			},
		},
	})
	crds := []object{}
	for _, name := range []string{"applications", buildsName, gatewayName} {
		sourceName := "loco-" + name + "-crd"
		ref, err := source(sourceName, manifest.Artifacts[sourceName], layerType)
		if err != nil {
			return nil, err
		}
		custom := resource(
			kindKustomization,
			sourceName,
			object{
				fieldInterval:        "5m",
				"sourceRef":          object{fieldKind: kindOCIRepository, fieldName: sourceName},
				"path":               "./",
				"prune":              false,
				"wait":               true,
				"timeout":            "5m",
				"serviceAccountName": reconcilerAccount,
				"postBuild":          object{"substitute": object{"PLATFORM_RELEASE": revision}},
			},
		)
		_, pinned, _ := strings.Cut(manifest.Artifacts[sourceName], "@")
		fields(custom, fieldMetadata)["annotations"] = object{"platform.loco.io/artifact-digest": pinned}
		crds = append(crds, ref, custom)
	}
	writeLayer(crdsName, crds)
	helmRelease := func(name, chart, namespace string, values object, dependencies []string) ([]object, error) {
		ref, err := source(name+"-chart", manifest.Artifacts[chart], helmLayerType)
		if err != nil {
			return nil, err
		}
		depends := []object{}
		for _, name := range dependencies {
			depends = append(depends, dependency(name))
		}
		valuesFrom := []object{}
		for _, entry := range []struct{ kind, suffix string }{
			{"ConfigMap", "-base-values"},
			{"ConfigMap", "-values"},
			{"Secret", "-secrets"},
		} {
			valuesFrom = append(
				valuesFrom,
				object{
					fieldKind:   entry.kind,
					fieldName:   name + entry.suffix,
					"valuesKey": "values.yaml",
					"optional":  true,
				},
			)
		}
		hr := resource(
			"HelmRelease",
			name,
			object{
				fieldInterval:        "5m",
				"serviceAccountName": reconcilerAccount,
				"releaseName":        name,
				"targetNamespace":    namespace,
				"chartRef":           object{fieldKind: kindOCIRepository, fieldName: name + "-chart"},
				"dependsOn":          depends,
				"valuesFrom":         valuesFrom,
				"values":             values,
				"install": object{
					"createNamespace": false,
					crdsName:          "Skip",
					"remediation":     object{"retries": 2},
				},
				"upgrade":        object{crdsName: "Skip", "remediation": object{"retries": 2, "strategy": "rollback"}},
				"driftDetection": object{"mode": fieldEnabled},
			},
		)
		fields(hr, fieldMetadata)["labels"] = labels
		fields(hr, fieldMetadata)["annotations"] = annotations
		return []object{ref, hr}, nil
	}
	ownedCRDs := func(objects []object) {
		spec := fields(objects[1], "spec")
		fields(spec, "install")[crdsName] = "Create"
		fields(spec, "upgrade")[crdsName] = "CreateReplace"
	}
	cert, err := helmRelease(
		certManagerName,
		"cert-manager-chart",
		certManagerName,
		object{crdsName: object{fieldEnabled: true, "keep": true}},
		nil,
	)
	if err != nil {
		return nil, err
	}
	ownedCRDs(cert)
	gateway, err := helmRelease(
		"envoy-gateway",
		"gateway-chart",
		"loco-system",
		object{
			crdsName: object{fieldEnabled: false},
			"config": object{
				"envoyGateway": object{
					"provider": object{
						"type":       "Kubernetes",
						"kubernetes": object{"deploy": object{"type": "GatewayNamespace"}},
					},
				},
			},
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	writeLayer(infraLayer, append(cert, gateway...))
	pinned := map[string]object{}
	for name, reference := range manifest.Images {
		repository, digest, ok := strings.Cut(reference, "@")
		if !ok || !digestPattern.MatchString(digest) {
			return nil, errors.New("image reference must contain a sha256 digest")
		}
		pinned[name] = object{"repository": repository, tagOperation: "sha-" + manifest.CoreCommit + "@" + digest}
	}
	for _, name := range images {
		if pinned[name] == nil {
			return nil, fmt.Errorf("missing image %s", name)
		}
	}
	cp, err := helmRelease(
		"loco-control-plane",
		"loco-control-plane",
		"loco-system",
		object{
			imageAPI:   object{fieldImage: pinned[imageAPI]},
			"ui":       object{fieldImage: pinned["ui"]},
			"obsProxy": object{fieldImage: pinned["obs-proxy"]},
		},
		[]string{certManagerName, "envoy-gateway"},
	)
	if err != nil {
		return nil, err
	}
	ownedCRDs(cp)
	writeLayer(controlPlaneName, cp)
	worker, err := helmRelease(
		"loco-worker",
		"loco-worker",
		"loco-system",
		object{
			imageAgent:      object{fieldImage: pinned[imageAgent]},
			imageController: object{fieldImage: pinned[imageController]},
			buildsName:      object{"builderImage": pinned["builder"]},
			gatewayChart:    object{fieldEnabled: false},
		},
		[]string{certManagerName, "envoy-gateway"},
	)
	if err != nil {
		return nil, err
	}
	writeLayer(workerLayer, worker)
	layers := map[string]object{}
	for _, entry := range []struct {
		name         string
		dependencies []string
	}{
		{namespacesLayer, nil},
		{crdsName, []string{namespacesLayer}},
		{infraLayer, []string{crdsName}},
		{controlPlaneName, []string{infraLayer}},
		{workerLayer, []string{infraLayer}},
	} {
		depends := []object{}
		for _, name := range entry.dependencies {
			depends = append(depends, dependency("loco-"+name))
		}
		spec := object{
			fieldInterval: "5m",
			"sourceRef":   object{fieldKind: kindOCIRepository, fieldName: "platform-bundle"},
			"path":        "./layers/" + entry.name,
			"prune":       entry.name != namespacesLayer && entry.name != crdsName,
			"wait":        true,
			"timeout":     "10m",
			"postBuild":   object{"substitute": object{"PLATFORM_RELEASE": revision}},
			"dependsOn":   depends,
		}
		if entry.name != namespacesLayer {
			spec["serviceAccountName"] = reconcilerAccount
		}
		if entry.name == controlPlaneName || entry.name == workerLayer {
			spec["dependsOn"] = append(depends, object{fieldName: "loco-cluster-config"})
		}
		if entry.name == crdsName {
			spec["healthCheckExprs"] = []object{
				{
					fieldAPIVersion: "kustomize.toolkit.fluxcd.io/v1",
					fieldKind:       kindKustomization,
					"current":       crdHealthExpression,
				},
			}
		}
		layer := resource(kindKustomization, "loco-"+entry.name, spec)
		fields(layer, fieldMetadata)["labels"] = labels
		fields(layer, fieldMetadata)["annotations"] = annotations
		layers[entry.name] = layer
	}
	for _, entry := range []struct {
		name     string
		selected []string
	}{
		{controlPlaneName, []string{controlPlaneName}},
		{workerLayer, []string{workerLayer}},
		{"all", []string{controlPlaneName, workerLayer}},
	} {
		paths := []string{}
		for _, name := range append([]string{namespacesLayer, crdsName, infraLayer}, entry.selected...) {
			file := name + ".yaml"
			output[entry.name+"/"+file] = encoded(layers[name])
			paths = append(paths, file)
		}
		output[entry.name+"/kustomization.yaml"] = kustomization(paths)
	}
	output["release.json"] = encoded(manifest)
	return output, nil
}
