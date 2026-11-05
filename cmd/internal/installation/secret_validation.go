package installation

import (
	"bytes"
	"encoding/base64"
	"net/url"

	corev1 "k8s.io/api/core/v1"
)

func validSeal(secret *corev1.Secret) bool {
	key, err := base64.StdEncoding.DecodeString(string(secret.Data[secretKeyField]))
	return err == nil && len(key) == sealKeyBytes
}

func validClickHouse(secret *corev1.Secret, role, host string) bool {
	parsed, err := url.Parse(string(secret.Data["url"]))
	if err != nil || parsed.Scheme != "clickhouse" || parsed.User == nil || parsed.Host != host || parsed.Path != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return false
	}
	password, ok := parsed.User.Password()
	return ok && parsed.User.Username() == "loco_"+role &&
		len(secret.Data[secretPasswordField]) >= minimumCredentialLength &&
		bytes.Equal(secret.Data[secretPasswordField], []byte(password))
}
