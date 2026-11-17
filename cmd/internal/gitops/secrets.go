package gitops

import "sigs.k8s.io/yaml"

func Secret(namespace, name string, data map[string]string) ([]byte, error) {
	return yaml.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]string{"name": name, "namespace": namespace},
		"type":     "Opaque", "stringData": data,
	})
}
