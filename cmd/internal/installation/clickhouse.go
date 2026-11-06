package installation

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// the pinned operator adds chi-, -deploy-confd-, a 15-character cluster and -0-0
const maxClickHouseNameLength = 26
const controlPlaneValuesPath = "loco-control-plane/values.yaml"

type managedClickHouse struct {
	Name             string
	OperatorName     string
	OperatorUsername string
}

func readManagedClickHouse(chart []byte) (managedClickHouse, error) {
	files, err := archiveFiles(chart)
	if err != nil {
		return managedClickHouse{}, err
	}
	var values struct {
		ClickHouse struct {
			Fullname string `json:"fullnameOverride"`
			Operator struct {
				Fullname string `json:"fullnameOverride"`
				Secret   struct {
					Create   *bool  `json:"create"`
					Username string `json:"username"`
				} `json:"secret"`
			} `json:"operator"`
		} `json:"clickhouse"`
	}
	if err := yaml.Unmarshal(files[controlPlaneValuesPath], &values); err != nil {
		return managedClickHouse{}, errInvalidRelease
	}
	if !label(values.ClickHouse.Fullname) || len(values.ClickHouse.Fullname) > maxClickHouseNameLength {
		return managedClickHouse{}, errInvalidRelease
	}
	operator := values.ClickHouse.Operator
	if !label(operator.Fullname) || operator.Secret.Create == nil || *operator.Secret.Create ||
		operator.Secret.Username == "" {
		return managedClickHouse{}, errInvalidRelease
	}
	return managedClickHouse{
		Name: values.ClickHouse.Fullname, OperatorName: operator.Fullname, OperatorUsername: operator.Secret.Username,
	}, nil
}

func (k *Kubernetes) ensureClickHouseOperator(
	ctx context.Context, state *corev1.ConfigMap, clickhouse managedClickHouse,
) error {
	password, err := randomCredential()
	if err != nil {
		return err
	}
	secret, err := k.ensureSecret(
		ctx, state, clickhouse.OperatorName,
		[]string{secretUsernameField, secretPasswordField},
		map[string][]byte{secretUsernameField: []byte(clickhouse.OperatorUsername), secretPasswordField: password},
	)
	if err != nil {
		return err
	}
	if string(secret.Data[secretUsernameField]) != clickhouse.OperatorUsername ||
		len(secret.Data[secretPasswordField]) < minimumCredentialLength {
		return errSecretInvalid
	}
	return nil
}
