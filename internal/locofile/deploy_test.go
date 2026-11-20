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
    deploy: {restart: on-failure, maxRestarts: 4, timeout: 120}
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
		if deploy == nil || deploy.Restart != RestartOnFailure || *deploy.MaxRestarts != 4 || *deploy.Timeout != 120 {
			t.Errorf("%s deploy = %+v", environment, deploy)
		}
	}
}

func TestDeployBounds(t *testing.T) {
	for deploy, want := range map[string]error{
		"{maxRestarts: 0}":                  ErrMaxRestarts,
		"{maxRestarts: 1}":                  nil,
		"{maxRestarts: 20}":                 nil,
		"{maxRestarts: 21}":                 ErrMaxRestarts,
		"{restart: always}":                 nil,
		"{restart: never}":                  nil,
		"{restart: never, maxRestarts: 3}":  ErrMaxRestartsMode,
		"{restart: always, maxRestarts: 3}": ErrMaxRestartsMode,
		"{restart: sometimes}":              ErrRestart,
		"{timeout: 59}":                     ErrDeployTimeout,
		"{timeout: 60}":                     nil,
		"{timeout: 3600}":                   nil,
		"{timeout: 3601}":                   ErrDeployTimeout,
	} {
		t.Run(deploy, func(t *testing.T) {
			file := strings.Replace(deployFile, "{restart: on-failure, maxRestarts: 4, timeout: 120}", deploy, 1)
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
