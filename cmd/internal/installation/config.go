package installation

const ConfigVersion = 1

// DefaultFile is the installation file locoadm commands read when --file is omitted.
const DefaultFile = "loco-install.yaml"

// FileFlagUsage is the help text of the --file flag on every locoadm command that reads DefaultFile.
const FileFlagUsage = "Installation configuration file"

type Config struct {
	Version      int          `json:"version"           yaml:"version"`
	Name         string       `json:"name"              yaml:"name"`
	Release      *Release     `json:"release,omitempty" yaml:"release,omitempty"`
	ControlPlane ControlPlane `json:"controlPlane"      yaml:"controlPlane"`
	Workers      []Worker     `json:"workers"           yaml:"workers"`
}

// Release names a published platform bundle version or, for air-gapped installs, a
// local release.json; exactly one is set. Without it, locoadm installs its own version.
type Release struct {
	Version  string `json:"version,omitempty"  yaml:"version,omitempty"`
	Manifest string `json:"manifest,omitempty" yaml:"manifest,omitempty"`
}

type KubernetesTarget struct {
	ClusterUID string `json:"clusterUID" yaml:"clusterUID"`
	Context    string `json:"context"    yaml:"context"`
	Namespace  string `json:"namespace"  yaml:"namespace"`
}

type SecretRef struct {
	Name string `json:"name" yaml:"name"`
}

type SecretKeyRef struct {
	Name string `json:"name" yaml:"name"`
	Key  string `json:"key"  yaml:"key"`
}

type TLS struct {
	MinVersion string `json:"minVersion" yaml:"minVersion"`
}

type ControlPlane struct {
	TLS           TLS              `json:"tls,omitzero"  yaml:"tls,omitempty"`
	Kubernetes    KubernetesTarget `json:"kubernetes"    yaml:"kubernetes"`
	APIURL        string           `json:"apiURL"        yaml:"apiURL"`
	WebURL        string           `json:"webURL"        yaml:"webURL"`
	Admins        []string         `json:"admins"        yaml:"admins"`
	Database      Database         `json:"database"      yaml:"database"`
	Auth          Auth             `json:"auth"          yaml:"auth"`
	SourceBucket  Bucket           `json:"sourceBucket"  yaml:"sourceBucket"`
	Registry      Registry         `json:"registry"      yaml:"registry"`
	OpenBao       OpenBao          `json:"openbao"       yaml:"openbao"`
	Observability Observability    `json:"observability" yaml:"observability"`
	Certificates  Certificates     `json:"certificates"  yaml:"certificates"`
}

type Database struct {
	SecretRef SecretKeyRef `json:"secretRef" yaml:"secretRef"`
}

type Auth struct {
	ClientID   string `json:"clientId"   yaml:"clientId"`
	Issuer     string `json:"issuer"     yaml:"issuer"`
	SignupMode string `json:"signupMode" yaml:"signupMode"`
}

type Bucket struct {
	Endpoint             string    `json:"endpoint"             yaml:"endpoint"`
	Region               string    `json:"region"               yaml:"region"`
	Bucket               string    `json:"bucket"               yaml:"bucket"`
	CredentialsSecretRef SecretRef `json:"credentialsSecretRef" yaml:"credentialsSecretRef"`
}

type Registry struct {
	Endpoint             string    `json:"endpoint"             yaml:"endpoint"`
	CredentialsSecretRef SecretRef `json:"credentialsSecretRef" yaml:"credentialsSecretRef"`
}

type OpenBao struct {
	Managed   bool   `json:"managed"   yaml:"managed"`
	Seal      Seal   `json:"seal"      yaml:"seal"`
	Snapshots Bucket `json:"snapshots" yaml:"snapshots"`
}

type Seal struct {
	Type string `json:"type" yaml:"type"`
}

type Observability struct {
	Enabled   bool   `json:"enabled"   yaml:"enabled"`
	IngestURL string `json:"ingestURL" yaml:"ingestURL"`
}

// Certificates sets exactly one of IssuerRef, an issuer the operator manages, or Issuer,
// a ClusterIssuer the Loco chart release creates.
type Certificates struct {
	IssuerRef *IssuerRef `json:"issuerRef,omitempty" yaml:"issuerRef,omitempty"`
	Issuer    *Issuer    `json:"issuer,omitempty"    yaml:"issuer,omitempty"`
}

type Issuer struct {
	Name       string     `json:"name"       yaml:"name"`
	SelfSigned SelfSigned `json:"selfSigned" yaml:"selfSigned"`
}

type SelfSigned struct{}

type IssuerRef struct {
	Name string `json:"name" yaml:"name"`
	Kind string `json:"kind" yaml:"kind"`
}

type Worker struct {
	TLS           TLS              `json:"tls,omitzero"  yaml:"tls,omitempty"`
	MetricsServer MetricsServer    `json:"metricsServer" yaml:"metricsServer"`
	Name          string           `json:"name"          yaml:"name"`
	Kubernetes    KubernetesTarget `json:"kubernetes"    yaml:"kubernetes"`
	Region        string           `json:"region"        yaml:"region"`
	Tier          string           `json:"tier"          yaml:"tier"`
	Role          string           `json:"role"          yaml:"role"`
	AppsDomain    string           `json:"appsDomain"    yaml:"appsDomain"`
	Builds        Builds           `json:"builds"        yaml:"builds"`
	Certificates  Certificates     `json:"certificates"  yaml:"certificates"`
}

// PrivateEgress matches the controller's build-network contract in
// controller/internal/builds/config.go; CLI and controller use separate Go modules.
type PrivateEgress struct {
	CIDR string `json:"cidr" yaml:"cidr"`
	Port int32  `json:"port" yaml:"port"`
}

type Builds struct {
	PrivateEgress []PrivateEgress `json:"privateEgress" yaml:"privateEgress"`
}

type MetricsServer struct {
	Enabled            bool `json:"enabled"            yaml:"enabled"`
	KubeletInsecureTLS bool `json:"kubeletInsecureTLS" yaml:"kubeletInsecureTLS"`
}
