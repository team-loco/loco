package cluster

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/version"
)

// The tested oldest image owns the startup minimum so the agent cannot silently
// accept clusters older than the test coverage.
//
//go:embed kubernetes.json
var kubernetesImages []byte

func (i *Inspector) CheckKubernetesVersion() error {
	server, err := i.client.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("discover Kubernetes server version: %w", err)
	}
	actual, err := version.ParseSemantic(server.GitVersion)
	if err != nil {
		return fmt.Errorf("parse Kubernetes server version %q: %w", server.GitVersion, err)
	}
	var images struct {
		Oldest string `json:"oldest"`
	}
	if err = json.Unmarshal(kubernetesImages, &images); err != nil {
		return fmt.Errorf("read Kubernetes support policy: %w", err)
	}
	_, tag, _ := strings.Cut(images.Oldest, ":v")
	tag, _, _ = strings.Cut(tag, "@")
	oldest, err := version.ParseSemantic(tag)
	if err != nil {
		return fmt.Errorf("parse oldest Kubernetes image: %w", err)
	}
	minimum := version.MustParseSemantic(fmt.Sprintf("%d.%d.0", oldest.Major(), oldest.Minor()))
	if actual.LessThan(minimum) {
		return fmt.Errorf(
			"kubernetes %s is unsupported: upgrade the API server to %s or newer "+
				"with RelaxedServiceNameValidation enabled; see "+
				"https://docs.loco.build/operations/self-hosting/#supported-kubernetes-versions",
			server.GitVersion,
			minimum,
		)
	}
	return nil
}
