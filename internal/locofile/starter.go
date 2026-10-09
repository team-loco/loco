package locofile

import (
	"bytes"
	"fmt"
	"text/template"
)

// Starter is what loco init fills into the first loco.yaml.
type Starter struct {
	SchemaURL   string
	Name        string
	Hostname    string
	Port        int32
	Region      string
	CPU         string
	Memory      string
	MinReplicas int32
	MaxReplicas int32
}

var starterTemplate = template.Must(template.New("loco.yaml").Parse(
	`# yaml-language-server: $schema={{.SchemaURL}}
version: {{.Version}}
partial: {{.Name}}

services:
  {{.Name}}:
    dockerfile: {{.Dockerfile}}
    port: {{.Port}}
    domains: [{{.Hostname}}]
    regions:
      {{.Region}}: { cpu: {{.CPU}}, memory: {{.Memory}}, replicas: { min: {{.MinReplicas}}, max: {{.MaxReplicas}} } }
`))

type starterValues struct {
	Starter
	Version    int
	Dockerfile string
}

// Render writes the starter file.
func (s Starter) Render() ([]byte, error) {
	values := starterValues{Starter: s, Version: Version, Dockerfile: DefaultDockerfile}
	var buf bytes.Buffer
	if err := starterTemplate.Execute(&buf, values); err != nil {
		return nil, fmt.Errorf("render starter: %w", err)
	}
	return buf.Bytes(), nil
}
