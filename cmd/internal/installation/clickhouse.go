package installation

import "sigs.k8s.io/yaml"

// the pinned operator adds chi-, -deploy-confd-, a 15-character cluster and -0-0
const maxClickHouseNameLength = 26
const controlPlaneValuesPath = "loco-control-plane/values.yaml"

func managedClickHouseName(chart []byte) (string, error) {
	files, err := archiveFiles(chart)
	if err != nil {
		return "", err
	}
	var values struct {
		ClickHouse struct {
			Fullname string `json:"fullnameOverride"`
		} `json:"clickhouse"`
	}
	if err := yaml.Unmarshal(files[controlPlaneValuesPath], &values); err != nil {
		return "", errInvalidRelease
	}
	if !label(values.ClickHouse.Fullname) || len(values.ClickHouse.Fullname) > maxClickHouseNameLength {
		return "", errInvalidRelease
	}
	return values.ClickHouse.Fullname, nil
}
