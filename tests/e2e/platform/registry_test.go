package platform

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestRegistryCredentialsCoverHostAndClusterPulls(t *testing.T) {
	hosts := []string{"localhost:5301", "loco-e2e-registry:5000"}
	for _, account := range []Account{RegistryNodesUser, RegistryBuilderUser} {
		data, err := registryDockerConfig(account, hosts)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Auths map[string]DockerConfigAuth `json:"auths"`
		}
		if err = json.Unmarshal([]byte(data), &config); err != nil {
			t.Fatal(err)
		}
		for _, host := range hosts {
			credentials, err := base64.StdEncoding.DecodeString(config.Auths[host].Auth)
			if err != nil || string(credentials) != account.Name+":"+account.Password {
				t.Fatalf("%s credentials for %s: %q, %v", account.Name, host, credentials, err)
			}
		}
	}
}
