package domains

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	wildcardPrefix       = "*."
	certificateReady     = "Ready"
	certificateDNSNames  = "dnsNames"
	certificateSpec      = "spec"
	certificateStatus    = "status"
	certificateCondition = "conditions"
)

var certificateKind = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}

type Config struct {
	Namespace   string
	Gateway     string
	Certificate string
	Domains     []string
}

type Reporter struct {
	reader client.Reader
	config Config
}

func NewReader(config *rest.Config) (client.Reader, error) {
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		return nil, fmt.Errorf("add gateway types to scheme: %w", err)
	}
	reader, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create gateway client: %w", err)
	}
	return reader, nil
}

func New(reader client.Reader, config Config) *Reporter {
	return &Reporter{reader: reader, config: config}
}

func (r *Reporter) Served(ctx context.Context) ([]string, error) {
	gateway := &gatewayv1.Gateway{}
	key := client.ObjectKey{Namespace: r.config.Namespace, Name: r.config.Gateway}
	err := r.reader.Get(ctx, key, gateway)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get gateway %s: %w", key, err)
	}
	certified, err := r.certifiedNames(ctx)
	if err != nil {
		return nil, err
	}
	var served []string
	for _, domain := range r.config.Domains {
		hostname := wildcardPrefix + domain
		if certified != nil && !slices.Contains(certified, hostname) {
			continue
		}
		if listenersReady(gateway, hostname) {
			served = append(served, domain)
		}
	}
	return served, nil
}

func (r *Reporter) certifiedNames(ctx context.Context) ([]string, error) {
	if r.config.Certificate == "" {
		return nil, nil
	}
	certificate := &unstructured.Unstructured{}
	certificate.SetGroupVersionKind(certificateKind)
	key := client.ObjectKey{Namespace: r.config.Namespace, Name: r.config.Certificate}
	err := r.reader.Get(ctx, key, certificate)
	if apierrors.IsNotFound(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get certificate %s: %w", key, err)
	}
	if !certificateIsReady(certificate) {
		return []string{}, nil
	}
	names, _, err := unstructured.NestedStringSlice(certificate.Object, certificateSpec, certificateDNSNames)
	if err != nil {
		return nil, fmt.Errorf("read certificate %s dns names: %w", key, err)
	}
	return append([]string{}, names...), nil
}

func certificateIsReady(certificate *unstructured.Unstructured) bool {
	conditions, _, err := unstructured.NestedSlice(certificate.Object, certificateStatus, certificateCondition)
	if err != nil {
		return false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if condition["type"] != certificateReady {
			continue
		}
		observed, found, err := unstructured.NestedInt64(condition, "observedGeneration")
		if err != nil || !found {
			return false
		}
		return condition["status"] == string(metav1.ConditionTrue) && observed == certificate.GetGeneration()
	}
	return false
}

func listenersReady(gateway *gatewayv1.Gateway, hostname string) bool {
	matched := false
	for _, listener := range gateway.Spec.Listeners {
		if listener.Hostname == nil || string(*listener.Hostname) != hostname {
			continue
		}
		matched = true
		if !listenerReady(gateway, listener.Name) {
			return false
		}
	}
	return matched
}

func listenerReady(gateway *gatewayv1.Gateway, name gatewayv1.SectionName) bool {
	index := slices.IndexFunc(gateway.Status.Listeners, func(status gatewayv1.ListenerStatus) bool {
		return status.Name == name
	})
	if index < 0 {
		return false
	}
	conditions := gateway.Status.Listeners[index].Conditions
	for _, conditionType := range []gatewayv1.ListenerConditionType{
		gatewayv1.ListenerConditionAccepted,
		gatewayv1.ListenerConditionProgrammed,
	} {
		condition := meta.FindStatusCondition(conditions, string(conditionType))
		if condition == nil || condition.Status != metav1.ConditionTrue ||
			condition.ObservedGeneration != gateway.Generation {
			return false
		}
	}
	return true
}
