package locofile

import "testing"

func TestNullOverrideRemovesHealth(t *testing.T) {
	file, err := Parse([]byte(`version: 1
partial: worker
services:
  worker:
    image: example.com/worker:v1
    health: {path: /healthz}
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
    environments:
      production:
        health: null
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"production", "staging"} {
		services, err := Resolve(file, environment)
		if err != nil {
			t.Fatal(err)
		}
		if (services["worker"].Health == nil) != (environment == "production") {
			t.Errorf("%s health = %+v", environment, services["worker"].Health)
		}
	}
}

func TestEmptyHealthObjectEnablesHTTPChecks(t *testing.T) {
	file, err := Parse([]byte(`version: 1
partial: web
services:
  web:
    image: example.com/web:v1
    health: {}
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
`))
	if err != nil {
		t.Fatal(err)
	}
	services, err := Resolve(file, "production")
	if err != nil {
		t.Fatal(err)
	}
	if services["web"].Health == nil {
		t.Fatal("explicit empty health object must enable HTTP checks")
	}
}
