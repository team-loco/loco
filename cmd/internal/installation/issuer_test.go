package installation

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

const (
	exampleIssuerRef = "issuerRef: {name: example-issuer, kind: ClusterIssuer}"
	declaredIssuer   = "issuer: {name: loco-issuer, selfSigned: {}}"
	testIssuer       = "loco-issuer"
	helmOwner        = `{"metadata":{"annotations":{"meta.helm.sh/release-name":"loco-worker",` +
		`"meta.helm.sh/release-namespace":%q}}}`
)

func TestCertificatesTakeExactlyOneIssuer(t *testing.T) {
	valid := string(example(t))
	for _, test := range []struct {
		name, controlPlane, worker string
		valid                      bool
	}{
		{"references", exampleIssuerRef, exampleIssuerRef, true},
		{"declared", declaredIssuer, declaredIssuer, true},
		{"mixed", declaredIssuer, exampleIssuerRef, true},
		{"both", exampleIssuerRef + "\n    " + declaredIssuer, exampleIssuerRef, false},
		{"neither", "{}", exampleIssuerRef, false},
		{"missing selfSigned", "issuer: {name: loco-issuer}", exampleIssuerRef, false},
		{"unsupported type", "issuer: {name: loco-issuer, acme: {}}", exampleIssuerRef, false},
		{"invalid name", "issuer: {name: Loco_Issuer, selfSigned: {}}", exampleIssuerRef, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			controlPlane := strings.Replace(valid, "    "+exampleIssuerRef, "    "+test.controlPlane, 1)
			if test.controlPlane == "{}" {
				controlPlane = strings.Replace(valid, "\n    "+exampleIssuerRef, "", 1)
				controlPlane = strings.Replace(controlPlane, "  certificates:\n", "  certificates: {}\n", 1)
			}
			data := strings.Replace(controlPlane, "      "+exampleIssuerRef, "      "+test.worker, 1)
			_, err := Parse([]byte(data))
			if (err == nil) != test.valid {
				t.Fatalf("valid = %t, error = %v", test.valid, err)
			}
		})
	}
}

func TestDeclaredIssuerBelongsToOneReleasePerCluster(t *testing.T) {
	data := strings.ReplaceAll(string(example(t)), exampleIssuerRef, declaredIssuer)
	shared := strings.Replace(data, "clusterUID: worker-cluster-uid", "clusterUID: control-plane-cluster-uid", 1)
	_, err := Parse([]byte(shared))
	if !errors.Is(err, errInvalid) || !strings.Contains(err.Error(), "workers[0].certificates.issuer.name") {
		t.Fatalf("got %v, want a rejected shared issuer name", err)
	}
	distinct := strings.Replace(shared, "name: loco-issuer", "name: loco-worker-issuer", 1)
	if _, err := Parse([]byte(distinct)); err != nil {
		t.Fatalf("distinct issuer names on one cluster: %v", err)
	}
}

func TestIssuerChartValues(t *testing.T) {
	for _, test := range []struct {
		name         string
		certificates Certificates
		want         string
	}{
		{
			"declared",
			Certificates{Issuer: &Issuer{Name: testIssuer}},
			"{create: true, type: selfSigned, kind: ClusterIssuer, name: loco-issuer}",
		},
		{
			"reference",
			Certificates{IssuerRef: &IssuerRef{Name: "operator-issuer", Kind: "Issuer"}},
			"{create: false, kind: Issuer, name: operator-issuer}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := Parse(example(t))
			if err != nil {
				t.Fatal(err)
			}
			config.ControlPlane.APIURL = "http://api.example.com"
			config.ControlPlane.Certificates = test.certificates
			worker := config.Workers[0]
			worker.Certificates = test.certificates
			controlPlane, err := controlPlaneValues(*config, releaseFixture())
			if err != nil {
				t.Fatal(err)
			}
			workerChart, err := workerValues(*config, worker, releaseFixture())
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := yaml.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatal(err)
			}
			for chart, rendered := range map[string][]byte{"control plane": controlPlane, "worker": workerChart} {
				var values struct {
					CertManager struct {
						Enabled bool           `json:"enabled"`
						Issuer  map[string]any `json:"issuer"`
					} `json:"certManager"`
				}
				if err := yaml.Unmarshal(rendered, &values); err != nil {
					t.Fatal(err)
				}
				if chart == "control plane" && values.CertManager.Enabled {
					t.Fatal("HTTP control plane must serve without a Gateway certificate")
				}
				if fmt.Sprint(values.CertManager.Issuer) != fmt.Sprint(want) {
					t.Fatalf("%s issuer values = %v, want %v", chart, values.CertManager.Issuer, want)
				}
			}
		})
	}
}

func TestDeclaredIssuerOwnership(t *testing.T) {
	const namespace = "loco-system"
	for _, test := range []struct {
		name     string
		status   int
		existing string
		want     error
	}{
		{"absent", http.StatusNotFound, "", nil},
		{
			"owned by the release", http.StatusOK,
			fmt.Sprintf(helmOwner, namespace),
			nil,
		},
		{
			"owned by another namespace", http.StatusOK,
			fmt.Sprintf(helmOwner, "other"),
			errIssuerConflict,
		},
		{"created by the operator", http.StatusOK, `{"metadata":{}}`, errIssuerConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != clusterIssuersPath+testIssuer {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", testJSON)
				w.WriteHeader(test.status)
				if test.status == http.StatusNotFound {
					fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
					return
				}
				fmt.Fprint(w, test.existing)
			}))
			defer server.Close()
			core, err := coreclient.NewForConfig(&rest.Config{Host: server.URL, ContentType: testJSON})
			if err != nil {
				t.Fatal(err)
			}
			kube := &Kubernetes{Core: core, Target: KubernetesTarget{Namespace: namespace}}
			err = checkIssuer(t.Context(), kube, Certificates{Issuer: &Issuer{Name: testIssuer}}, workerRelease)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}
