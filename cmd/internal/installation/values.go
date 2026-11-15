package installation

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"text/template"
)

const schemeHTTP = "http"
const schemeHTTPS = "https"

var valuesFunctions = template.FuncMap{"quote": strconv.Quote, "image": imageValues, "issuer": issuerValues}
var controlPlaneTemplate = template.Must(template.New("control-plane").Funcs(valuesFunctions).Parse(`
api:
  image: {{with image .Manifest "api"}}{repository: {{quote .repository}}, tag: {{quote .tag}}}{{end}}
  existingSecret: loco-api-env
  systemAdmins: [{{range $i, $admin := .Config.ControlPlane.Admins}}{{if $i}}, {{end}}{{quote $admin}}{{end}}]
  env:
    WEB_URL: {{quote .Config.ControlPlane.WebURL}}
    CORS_ALLOWED_ORIGINS: {{quote .Config.ControlPlane.WebURL}}
    AUTH_ISSUERS: {{quote .Issuer}}
    AUTH_SIGNUP_MODE: {{quote .Config.ControlPlane.Auth.SignupMode}}
    LOCO_SOURCE_BUCKET_ENDPOINT: {{quote .Config.ControlPlane.SourceBucket.Endpoint}}
    LOCO_SOURCE_BUCKET: {{quote .Config.ControlPlane.SourceBucket.Bucket}}
    LOCO_SOURCE_BUCKET_REGION: {{quote .Config.ControlPlane.SourceBucket.Region}}
    LOCO_REGISTRY_HOST: {{quote .RegistryHost}}
    LOCO_REGISTRY_URL: {{quote .Config.ControlPlane.Registry.Endpoint}}
ui:
  hostname: {{quote .WebHost}}
  image: {{with image .Manifest "ui"}}{repository: {{quote .repository}}, tag: {{quote .tag}}}{{end}}
controlPlaneHostname: {{quote .APIHost}}
gatewayNamespace: {{quote .Config.ControlPlane.Kubernetes.Namespace}}
certManager:
  enabled: {{.TLS}}
  issuer: {{issuer .Config.ControlPlane.Certificates}}
openbao:
  seal:
    static:
      existingSecret: {name: loco-openbao-seal, key: key}
  snapshots:
    s3:
      endpoint: {{quote .Config.ControlPlane.OpenBao.Snapshots.Endpoint}}
      region: {{quote .Config.ControlPlane.OpenBao.Snapshots.Region}}
      bucket: {{quote .Config.ControlPlane.OpenBao.Snapshots.Bucket}}
      existingSecret: {name: {{quote .Config.ControlPlane.OpenBao.Snapshots.CredentialsSecretRef.Name}}}
clickhouse:
  enabled: true
obsProxy:
  enabled: true
  image: {{with image .Manifest "obs-proxy"}}{repository: {{quote .repository}}, tag: {{quote .tag}}}{{end}}
  ingest:
    hostname: {{quote .IngestHost}}
`))

func controlPlaneValues(config Config, manifest Manifest) ([]byte, error) {
	cp := config.ControlPlane
	issuer, err := json.Marshal(
		[]map[string]any{
			{
				"name":     "installation",
				"issuer":   cp.Auth.Issuer,
				"audience": cp.Auth.ClientID,
				"web":      map[string]string{"clientId": cp.Auth.ClientID, "scopes": "openid profile email"},
			},
		},
	)
	if err != nil {
		return nil, err
	}
	data := struct {
		Config                                             Config
		Manifest                                           Manifest
		Issuer, APIHost, WebHost, IngestHost, RegistryHost string
		TLS                                                bool
	}{
		Config: config, Manifest: manifest, Issuer: string(issuer),
		APIHost: validatedURL(cp.APIURL).Hostname(), WebHost: validatedURL(cp.WebURL).Hostname(),
		IngestHost: validatedURL(cp.Observability.IngestURL).Hostname(), RegistryHost: registryHost(cp.Registry),
		TLS: validatedURL(cp.APIURL).Scheme == schemeHTTPS,
	}
	var output bytes.Buffer
	if err := controlPlaneTemplate.Execute(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

var workerTemplate = template.Must(template.New("worker").Funcs(valuesFunctions).Parse(`
metrics-server:
  enabled: {{.Worker.MetricsServer.Enabled}}
  args: {{if .Worker.MetricsServer.KubeletInsecureTLS}}["--kubelet-insecure-tls"]{{else}}[]{{end}}
agent:
  image: {{with image .Manifest "agent"}}{repository: {{quote .repository}}, tag: {{quote .tag}}}{{end}}
  joinToken: {existingSecret: loco-worker-enrollment}
controller:
  image: {{with image .Manifest "controller"}}{repository: {{quote .repository}}, tag: {{quote .tag}}}{{end}}
cluster:
  role: {{quote .Worker.Role}}
builds:
  builderImage: {{with image .Manifest "builder"}}{repository: {{quote .repository}}, tag: {{quote .tag}}}{{end}}
  privateEgress:{{if .Worker.Builds.PrivateEgress}}
{{range .Worker.Builds.PrivateEgress}}    - cidr: {{quote .CIDR}}
      port: {{.Port}}
{{end}}{{else}} []
{{end}}  pushSecretName: loco-registry-push
  insecureRegistry: {{.InsecureRegistry}}
registry:
  pullSecretName: loco-registry-pull
global:
  region: {{quote .Worker.Region}}
  domain:
    apps: [{{quote .Worker.AppsDomain}}]
  exporter:
    endpoint: {{quote .Config.ControlPlane.Observability.IngestURL}}
    ingestToken: {enabled: true}
gateway-helm:
  enabled: false
env:
  CONTROL_PLANE_URL: {{quote .Config.ControlPlane.APIURL}}
certManager:
  issuer: {{issuer .Worker.Certificates}}
`))

func workerValues(config Config, worker Worker, manifest Manifest) ([]byte, error) {
	data := struct {
		Config           Config
		Worker           Worker
		Manifest         Manifest
		InsecureRegistry bool
	}{config, worker, manifest, validatedURL(config.ControlPlane.Registry.Endpoint).Scheme == schemeHTTP}
	var output bytes.Buffer
	if err := workerTemplate.Execute(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func imageValues(manifest Manifest, key string) map[string]string {
	parts := strings.SplitN(manifest.Images[key], "@", 2)
	return map[string]string{"repository": parts[0], "tag": "sha-" + manifest.CoreCommit + "@" + parts[1]}
}
