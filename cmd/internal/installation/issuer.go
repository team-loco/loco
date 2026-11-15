package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	kindClusterIssuer  = "ClusterIssuer"
	clusterIssuersPath = "/apis/cert-manager.io/v1/clusterissuers/"
	// Helm records the owning release on every resource it installs.
	helmReleaseName      = "meta.helm.sh/release-name"
	helmReleaseNamespace = "meta.helm.sh/release-namespace"
)

var ErrIssuerRequired = errors.New(
	"configured certificate issuer is missing or not Ready; create it and run apply again",
)

var errIssuerConflict = errors.New(
	"a ClusterIssuer with the declared name exists and belongs to someone else; rename the issuer or use issuerRef",
)

// checkIssuer confirms that release can install against the configured issuer: a referenced
// issuer must be Ready, and a declared one must be absent or already belong to release.
func checkIssuer(ctx context.Context, kube *Kubernetes, certificates Certificates, release string) error {
	if certificates.IssuerRef != nil {
		return validateIssuer(ctx, kube, *certificates.IssuerRef)
	}
	data, err := kube.Core.RESTClient().Get().AbsPath(clusterIssuersPath + certificates.Issuer.Name).Do(ctx).Raw()
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return safeKubernetesError("read certificate issuer", err)
	}
	var resource struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &resource); err != nil {
		return fmt.Errorf("decode certificate issuer: %w", err)
	}
	annotations := resource.Metadata.Annotations
	if annotations[helmReleaseName] != release || annotations[helmReleaseNamespace] != kube.Target.Namespace {
		return errIssuerConflict
	}
	return nil
}

func validateIssuer(ctx context.Context, kube *Kubernetes, issuer IssuerRef) error {
	path := clusterIssuersPath + issuer.Name
	if issuer.Kind == "Issuer" {
		path = "/apis/cert-manager.io/v1/namespaces/" + kube.Target.Namespace + "/issuers/" + issuer.Name
	}
	data, err := kube.Core.RESTClient().Get().AbsPath(path).Do(ctx).Raw()
	if apierrors.IsNotFound(err) {
		return ErrIssuerRequired
	}
	if err != nil {
		return safeKubernetesError("read certificate issuer", err)
	}
	var resource struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &resource); err != nil {
		return ErrIssuerRequired
	}
	for _, condition := range resource.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return nil
		}
	}
	return ErrIssuerRequired
}

// issuerValues renders the chart's certManager.issuer values; JSON is valid YAML.
func issuerValues(certificates Certificates) (string, error) {
	values := map[string]any{"create": false}
	if certificates.Issuer != nil {
		values = map[string]any{
			"create": true, "type": "selfSigned", "kind": kindClusterIssuer, "name": certificates.Issuer.Name,
		}
	} else {
		values["kind"] = certificates.IssuerRef.Kind
		values["name"] = certificates.IssuerRef.Name
	}
	data, err := json.Marshal(values)
	return string(data), err
}
