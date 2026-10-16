package service

import (
	"testing"

	"buf.build/go/protovalidate"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

func TestCreateBuildPaths(t *testing.T) {
	cases := []struct {
		name       string
		dockerfile string
		context    string
		valid      bool
	}{
		{name: "default context", dockerfile: testDockerfile, valid: true},
		{name: "root context", dockerfile: testDockerfile, context: ".", valid: true},
		{name: "nested", dockerfile: "deploy/Dockerfile", context: "services/api", valid: true},
		{name: "spaces", dockerfile: "deploy files/Dockerfile", context: "service source", valid: true},
		{name: "empty dockerfile", context: "."},
		{name: "absolute dockerfile", dockerfile: "/Dockerfile", context: "."},
		{name: "parent dockerfile", dockerfile: "../Dockerfile", context: "."},
		{name: "nested parent dockerfile", dockerfile: "deploy/../Dockerfile", context: "."},
		{name: "dot dockerfile", dockerfile: ".", context: "."},
		{name: "dot segment dockerfile", dockerfile: "./Dockerfile", context: "."},
		{name: "empty segment dockerfile", dockerfile: "deploy//Dockerfile", context: "."},
		{name: "trailing slash dockerfile", dockerfile: "deploy/", context: "."},
		{name: "absolute context", dockerfile: testDockerfile, context: "/services"},
		{name: "parent context", dockerfile: testDockerfile, context: "../services"},
		{name: "nested parent context", dockerfile: testDockerfile, context: "services/../api"},
		{name: "dot segment context", dockerfile: testDockerfile, context: "./services"},
		{name: "empty segment context", dockerfile: testDockerfile, context: "services//api"},
		{name: "trailing slash context", dockerfile: testDockerfile, context: "services/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := &buildv1.CreateBuildRequest{
				ResourceId:     "00000000-0000-7000-8000-000000000001",
				DockerfilePath: tc.dockerfile,
				Context:        tc.context,
				SourceSize:     1,
			}
			err := protovalidate.Validate(request)
			if (err == nil) != tc.valid {
				t.Fatalf("dockerfile=%q context=%q valid=%t: %v", tc.dockerfile, tc.context, tc.valid, err)
			}
		})
	}
}
