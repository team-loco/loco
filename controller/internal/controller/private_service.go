package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelPrivateServiceOwner      = "loco.io/private-service-owner"
	componentPrivateService       = "private-service"
	privateServicePortName        = "tcp"
	privateServiceDefaultPortName = "default"
)

var (
	errPrivateServiceCollision    = errors.New("private service name is already in use by another owner")
	errPrivateServiceOwnerMissing = errors.New("private service requires an Application UID")
	errPrivateServiceType         = errors.New("owned private service must have type ClusterIP")
)

func ownsPrivateService(app *locov1alpha1.Application, svc *corev1.Service) bool {
	return app.UID != "" &&
		svc.Labels[labelPrivateServiceOwner] == string(app.UID) &&
		svc.Labels[locolabels.LabelManagedBy] == locolabels.ManagedByController &&
		svc.Labels[locolabels.LabelComponent] == componentPrivateService &&
		svc.Annotations[annotationAppNamespace] == app.Namespace &&
		svc.Annotations[annotationAppName] == app.Name
}

func (r *LocoResourceReconciler) ensurePrivateService(ctx context.Context, app *locov1alpha1.Application) error {
	name := locov1alpha1.PrivateServiceName(app.Spec.ResourceName)
	if name == "" {
		return r.prunePrivateServices(ctx, app, "")
	}
	if app.UID == "" {
		return errPrivateServiceOwnerMissing
	}
	namespace := r.workspaceNamespace(app)
	port := app.Spec.ServiceSpec.Deployment.Port
	targetPort := intstr.FromInt32(port)
	ports := []corev1.ServicePort{
		{Name: privateServiceDefaultPortName, Protocol: corev1.ProtocolTCP, Port: servicePort, TargetPort: targetPort},
	}
	if port != servicePort {
		ports = append(
			ports,
			corev1.ServicePort{
				Name:       privateServicePortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       port,
				TargetPort: targetPort,
			},
		)
	}
	desired := &corev1.Service{Name: name, Namespace: namespace,
		Labels: map[string]string{
			locolabels.LabelManagedBy: locolabels.ManagedByController,
			locolabels.LabelComponent: componentPrivateService,
			labelPrivateServiceOwner:  string(app.UID),
		}, Annotations: ownerAnnotations(app),
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{labelApp: getName(app)},
			Ports:    ports,
		},
	}
	current := &corev1.Service{}
	key := client.ObjectKeyFromObject(desired)
	reader := r.apiReader()
	err := reader.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("create private service %s: %w", key, createErr)
		}
	} else {
		if err != nil {
			return fmt.Errorf("get private service %s: %w", key, err)
		}
		if !ownsPrivateService(app, current) {
			return fmt.Errorf("%w: %s", errPrivateServiceCollision, key)
		}
		if current.Spec.Type != corev1.ServiceTypeClusterIP {
			return fmt.Errorf("%w: %s", errPrivateServiceType, key)
		}
		if !equality.Semantic.DeepEqual(current.Spec.Selector, desired.Spec.Selector) ||
			!equality.Semantic.DeepEqual(current.Spec.Ports, desired.Spec.Ports) {
			current.Spec.Selector = desired.Spec.Selector
			current.Spec.Ports = desired.Spec.Ports
			if updateErr := r.Update(ctx, current); updateErr != nil {
				return fmt.Errorf("update private service %s: %w", key, updateErr)
			}
		}
	}
	return r.prunePrivateServices(ctx, app, name)
}

func (r *LocoResourceReconciler) prunePrivateServices(
	ctx context.Context,
	app *locov1alpha1.Application,
	keep string,
) error {
	if app.UID == "" {
		return nil
	}
	list := &corev1.ServiceList{}
	selector := client.MatchingLabels{
		labelPrivateServiceOwner:  string(app.UID),
		locolabels.LabelComponent: componentPrivateService,
	}
	reader := r.apiReader()
	if err := reader.List(ctx, list, client.InNamespace(r.workspaceNamespace(app)), selector); err != nil {
		return fmt.Errorf("list private services: %w", err)
	}
	for i := range list.Items {
		svc := &list.Items[i]
		if svc.Name == keep || !ownsPrivateService(app, svc) {
			continue
		}
		preconditions := client.Preconditions{UID: &svc.UID, ResourceVersion: &svc.ResourceVersion}
		if err := r.Delete(ctx, svc, preconditions); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete private service %s: %w", svc.Name, err)
		}
	}
	return nil
}

func (r *LocoResourceReconciler) apiReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}
