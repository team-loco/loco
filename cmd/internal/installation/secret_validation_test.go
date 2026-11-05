package installation

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestExistingSealRequiresValidKeyMaterial(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{base64.StdEncoding.EncodeToString(make([]byte, 32)), true},
		{"not-base64", false},
		{base64.StdEncoding.EncodeToString(make([]byte, 31)), false},
	} {
		if got := validSeal(
			&corev1.Secret{Data: map[string][]byte{secretKeyField: []byte(test.value)}},
		); got != test.valid {
			t.Fatalf("validSeal=%v, want %v", got, test.valid)
		}
	}
}

func TestExistingClickHouseCredentialMatchesRoleAndURL(t *testing.T) {
	password := strings.Repeat("a", 32)
	endpoint := url.URL{
		Scheme: "clickhouse",
		Host:   "clickhouse.example:9000",
		User:   url.UserPassword("loco_reader", password),
	}
	secret := &corev1.Secret{
		Data: map[string][]byte{secretPasswordField: []byte(password), "url": []byte(endpoint.String())},
	}
	if !validClickHouse(secret, "reader", endpoint.Host) {
		t.Fatal("valid role credential rejected")
	}
	if validClickHouse(secret, "ingest", endpoint.Host) {
		t.Fatal("credential for another role accepted")
	}
	secret.Data[secretPasswordField] = []byte("different")
	if validClickHouse(secret, "reader", endpoint.Host) {
		t.Fatal("mismatched password accepted")
	}
}
