package locofile

import (
	"errors"
	"strings"
	"testing"
)

const (
	deployOverriddenEnv = "live"
	deployOtherEnv      = "preview"
)

const deployFile = `version: 1
partial: web
services:
  web:
    image: example.com/web:v1
    deploy: {attempts: 4, retryDelay: 120}
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
    environments:
      live:
        port: 8080
`

func TestDeployAppliesToEveryEnvironment(t *testing.T) {
	file, err := Parse([]byte(deployFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{deployOverriddenEnv, deployOtherEnv} {
		services, err := Resolve(file, environment)
		if err != nil {
			t.Fatal(err)
		}
		deploy := services["web"].Deploy
		if deploy == nil || *deploy.Attempts != 4 || *deploy.RetryDelay != 120 {
			t.Errorf("%s deploy = %+v", environment, deploy)
		}
	}
}

func TestDeployRejectsNonPositiveValues(t *testing.T) {
	for name, deploy := range map[string]string{
		"zero attempts":        "{attempts: 0}",
		"negative attempts":    "{attempts: -1}",
		"zero retry delay":     "{retryDelay: 0}",
		"negative retry delay": "{retryDelay: -5}",
	} {
		t.Run(name, func(t *testing.T) {
			file := strings.Replace(deployFile, "{attempts: 4, retryDelay: 120}", deploy, 1)
			if _, err := Parse([]byte(file)); !errors.Is(err, ErrNotPositive) {
				t.Fatalf("Parse = %v, want %v", err, ErrNotPositive)
			}
		})
	}
}

func TestDeployCannotBeOverriddenPerEnvironment(t *testing.T) {
	file := strings.Replace(deployFile, "        port: 8080", "        deploy: {attempts: 1}", 1)
	if _, err := Parse([]byte(file)); !errors.Is(err, errUnknownKey) {
		t.Fatalf("Parse = %v, want %v", err, errUnknownKey)
	}
}
