package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	locoNamespace  = "loco-system"
	platformDomain = "e2e.test.local"
	clusterRegion  = "local-1"
	// bucketRegion is the S3 region the source bucket signs requests for.
	bucketRegion  = "us-east-1"
	clusterName   = "e2e-test-cluster"
	workerRelease = "loco-worker"
	// gatewayName is loco-worker.gateway in charts/loco-worker/templates/_helpers.tpl.
	gatewayName = "eg"
)

// values are the chart values the run reads, so it starts the same images and settings the
// charts install.
type values struct {
	workspacePrefix   string
	buildNamespace    string
	buildkitImage     string
	clickhouseImage   string
	clickhouseKeeper  string
	clickhouseDB      string
	openbaoImage      string
	ingestPort        int
	ingestGRPCPort    int
	ingestHTTPPort    int
	ingestTokenSecret string
	ingestTokenKey    string
	collectorNS       string
	nodeCollectorNS   string
	collectorName     string
	collectorHTTPPort int
	agentLogLevel     string
}

type workerValues struct {
	Global struct {
		WorkspaceNamespacePrefix string `json:"workspaceNamespacePrefix"`
		Exporter                 struct {
			IngestToken struct {
				SecretName string `json:"secretName"`
				SecretKey  string `json:"secretKey"`
			} `json:"ingestToken"`
		} `json:"exporter"`
	} `json:"global"`
	Builds struct {
		Namespace     string `json:"namespace"`
		BuildkitImage image  `json:"buildkitImage"`
	} `json:"builds"`
	Env struct {
		LogLevel string `json:"LOG_LEVEL"`
	} `json:"env"`
	CollectorDeploy struct {
		NamespaceOverride string `json:"namespaceOverride"`
		FullnameOverride  string `json:"fullnameOverride"`
		Ports             struct {
			OTLPHTTP struct {
				ServicePort int `json:"servicePort"`
			} `json:"otlp-http"`
		} `json:"ports"`
	} `json:"otel-col-deploy"`
	CollectorDaemon struct {
		NamespaceOverride string `json:"namespaceOverride"`
	} `json:"otel-col-daemon"`
}

type controlPlaneValues struct {
	ClickHouse struct {
		ClickHouse struct {
			Image        image  `json:"image"`
			Database     string `json:"database"`
			KeeperConfig string `json:"keeperConfig"`
		} `json:"clickhouse"`
	} `json:"clickhouse"`
	ObsProxy struct {
		Ingest struct {
			Port     int `json:"port"`
			GRPCPort int `json:"grpcPort"`
			HTTPPort int `json:"httpPort"`
		} `json:"ingest"`
	} `json:"obsProxy"`
	OpenBao struct {
		Image string `json:"image"`
	} `json:"openbao"`
}

type image struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

func (i image) String() string {
	return i.Repository + ":" + i.Tag
}

func readValues(root string) values {
	var worker workerValues
	if err := ReadYAML(filepath.Join(root, "charts", "loco-worker", "values.yaml"), &worker); err != nil {
		panic(err)
	}
	var controlPlane controlPlaneValues
	if err := ReadYAML(filepath.Join(root, "charts", "loco-control-plane", "values.yaml"), &controlPlane); err != nil {
		panic(err)
	}
	return values{
		workspacePrefix:   worker.Global.WorkspaceNamespacePrefix,
		buildNamespace:    worker.Builds.Namespace,
		buildkitImage:     worker.Builds.BuildkitImage.String(),
		clickhouseImage:   controlPlane.ClickHouse.ClickHouse.Image.String(),
		clickhouseKeeper:  controlPlane.ClickHouse.ClickHouse.KeeperConfig,
		clickhouseDB:      controlPlane.ClickHouse.ClickHouse.Database,
		openbaoImage:      controlPlane.OpenBao.Image,
		ingestPort:        controlPlane.ObsProxy.Ingest.Port,
		ingestGRPCPort:    controlPlane.ObsProxy.Ingest.GRPCPort,
		ingestHTTPPort:    controlPlane.ObsProxy.Ingest.HTTPPort,
		ingestTokenSecret: worker.Global.Exporter.IngestToken.SecretName,
		ingestTokenKey:    worker.Global.Exporter.IngestToken.SecretKey,
		collectorNS:       worker.CollectorDeploy.NamespaceOverride,
		nodeCollectorNS:   worker.CollectorDaemon.NamespaceOverride,
		collectorName:     worker.CollectorDeploy.FullnameOverride,
		collectorHTTPPort: worker.CollectorDeploy.Ports.OTLPHTTP.ServicePort,
		agentLogLevel:     worker.Env.LogLevel,
	}
}

// ReadYAML decodes the YAML or JSON file at path into into.
func ReadYAML(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, into); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// fromImage reads the image of a fixture Dockerfile's first FROM line.
func fromImage(root, dockerfile string) string {
	return fromImages(filepath.Join(root, dockerfile))[0]
}

// fromImages reads the images of a Dockerfile's FROM lines.
func fromImages(dockerfile string) []string {
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		panic(err)
	}
	var images []string
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "FROM" {
			images = append(images, fields[1])
		}
	}
	if len(images) == 0 {
		panic(dockerfile + " has no FROM line")
	}
	return images
}

// kindNodeImage is the node image of E2E_KUBERNETES_VERSION, oldest or newest.
func kindNodeImage(root string) string {
	var versions map[string]string
	if err := ReadYAML(filepath.Join(root, "agent", "pkg", "cluster", "kubernetes.json"), &versions); err != nil {
		panic(err)
	}
	version := envOr("E2E_KUBERNETES_VERSION", "newest")
	node, ok := versions[version]
	if !ok {
		panic("E2E_KUBERNETES_VERSION must be oldest or newest, got " + version)
	}
	return node
}
