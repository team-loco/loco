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

func TestDeployBounds(t *testing.T) {
	for deploy, want := range map[string]error{
		"{attempts: 0}":     ErrDeployAttempts,
		"{attempts: 1}":     nil,
		"{attempts: 10}":    nil,
		"{attempts: 11}":    ErrDeployAttempts,
		"{retryDelay: 59}":  ErrDeployRetryDelay,
		"{retryDelay: 60}":  nil,
		"{retryDelay: -60}": ErrDeployRetryDelay,
	} {
		t.Run(deploy, func(t *testing.T) {
			file := strings.Replace(deployFile, "{attempts: 4, retryDelay: 120}", deploy, 1)
			if _, err := Parse([]byte(file)); !errors.Is(err, want) || (want == nil && err != nil) {
				t.Fatalf("Parse = %v, want %v", err, want)
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
