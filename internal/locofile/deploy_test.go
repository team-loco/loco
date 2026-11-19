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
    deploy: {maxRestarts: 4, restartWindow: 120}
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
		if deploy == nil || *deploy.MaxRestarts != 4 || *deploy.RestartWindow != 120 {
			t.Errorf("%s deploy = %+v", environment, deploy)
		}
	}
}

func TestDeployBounds(t *testing.T) {
	for deploy, want := range map[string]error{
		"{maxRestarts: -1}":     ErrMaxRestarts,
		"{maxRestarts: 0}":      nil,
		"{maxRestarts: 20}":     nil,
		"{maxRestarts: 21}":     ErrMaxRestarts,
		"{restartWindow: 59}":   ErrRestartWindow,
		"{restartWindow: 60}":   nil,
		"{restartWindow: 3600}": nil,
		"{restartWindow: 3601}": ErrRestartWindow,
	} {
		t.Run(deploy, func(t *testing.T) {
			file := strings.Replace(deployFile, "{maxRestarts: 4, restartWindow: 120}", deploy, 1)
			if _, err := Parse([]byte(file)); !errors.Is(err, want) || (want == nil && err != nil) {
				t.Fatalf("Parse = %v, want %v", err, want)
			}
		})
	}
}

func TestDeployCannotBeOverriddenPerEnvironment(t *testing.T) {
	file := strings.Replace(deployFile, "        port: 8080", "        deploy: {maxRestarts: 1}", 1)
	if _, err := Parse([]byte(file)); !errors.Is(err, errUnknownKey) {
		t.Fatalf("Parse = %v, want %v", err, errUnknownKey)
	}
}
