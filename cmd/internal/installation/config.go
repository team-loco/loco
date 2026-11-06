package installation

const ConfigVersion = 1

type Config struct {
	Version      int          `json:"version"      yaml:"version"`
	Name         string       `json:"name"         yaml:"name"`
	Release      Release      `json:"release"      yaml:"release"`
	ControlPlane ControlPlane `json:"controlPlane" yaml:"controlPlane"`
	Workers      []Worker     `json:"workers"      yaml:"workers"`
}

type Release struct {
	Manifest string `json:"manifest" yaml:"manifest"`
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

type ControlPlane struct {
	Kubernetes    KubernetesTarget `json:"kubernetes"    yaml:"kubernetes"`
	APIURL        string           `json:"apiURL"        yaml:"apiURL"`
	WebURL        string           `json:"webURL"        yaml:"webURL"`
	Database      Database         `json:"database"      yaml:"database"`
	Auth          Auth             `json:"auth"          yaml:"auth"`
	SourceBucket  Bucket           `json:"sourceBucket"  yaml:"sourceBucket"`
	Registry      Registry         `json:"registry"      yaml:"registry"`
	Email         Email            `json:"email"         yaml:"email"`
	OpenBao       OpenBao          `json:"openbao"       yaml:"openbao"`
	Observability Observability    `json:"observability" yaml:"observability"`
	Certificates  Certificates     `json:"certificates"  yaml:"certificates"`
}

type Database struct {
	SecretRef SecretKeyRef `json:"secretRef" yaml:"secretRef"`
}

type Auth struct {
	ClientID             string `json:"clientId"             yaml:"clientId"`
	BootstrapRedirectURI string `json:"bootstrapRedirectURI" yaml:"bootstrapRedirectURI"`
	Issuer               string `json:"issuer"               yaml:"issuer"`
	SignupMode           string `json:"signupMode"           yaml:"signupMode"`
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

type Email struct {
	From               string       `json:"from"               yaml:"from"`
	TransportSecretRef SecretKeyRef `json:"transportSecretRef" yaml:"transportSecretRef"`
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

type Certificates struct {
	IssuerRef IssuerRef `json:"issuerRef" yaml:"issuerRef"`
}

type IssuerRef struct {
	Name string `json:"name" yaml:"name"`
	Kind string `json:"kind" yaml:"kind"`
}

type Worker struct {
	Name         string           `json:"name"         yaml:"name"`
	Kubernetes   KubernetesTarget `json:"kubernetes"   yaml:"kubernetes"`
	Region       string           `json:"region"       yaml:"region"`
	Tier         string           `json:"tier"         yaml:"tier"`
	AppsDomain   string           `json:"appsDomain"   yaml:"appsDomain"`
	Builds       Builds           `json:"builds"       yaml:"builds"`
	Certificates Certificates     `json:"certificates" yaml:"certificates"`
}

// PrivateEgress matches the controller's build-network contract in
// controller/internal/builds/config.go; CLI and controller use separate Go modules.
type PrivateEgress struct {
	CIDR string `json:"cidr" yaml:"cidr"`
	Port int32  `json:"port" yaml:"port"`
}

type Builds struct {
	PrivateEgress []PrivateEgress `json:"privateEgress" yaml:"privateEgress"`
	Enabled       bool            `json:"enabled"       yaml:"enabled"`
}
