package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const envoyGatewayController gatewayv1.GatewayController = "gateway.envoyproxy.io/gatewayclass-controller"

func (r *ApplicationReconciler) observePublicRoute(
	ctx context.Context,
	app *loco.Application,
	desired *gatewayac.HTTPRouteSpecApplyConfiguration,
) error {
	revision, _ := app.PlacementRevision()
	observation := &loco.PublicRouteStatus{
		Revision:  revision,
		Hostnames: []string{app.Spec.ServiceSpec.Routing.HostName},
	}
	app.Status.PublicRoute = observation
	current := &gatewayv1.HTTPRoute{}
	key := client.ObjectKey{Namespace: r.environmentNamespace(app), Name: getRouteName(app)}
	if err := r.APIReader.Get(ctx, key, current); err != nil {
		return fmt.Errorf("read public route %s: %w", key, err)
	}
	encoded, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("encode desired public route: %w", err)
	}
	var expected gatewayv1.HTTPRouteSpec
	if err := json.Unmarshal(encoded, &expected); err != nil {
		return fmt.Errorf("decode desired public route: %w", err)
	}
	if !equality.Semantic.DeepEqual(current.Spec, expected) {
		return nil
	}
	for _, parent := range current.Status.Parents {
		if parent.ControllerName != envoyGatewayController {
			continue
		}
		if !equality.Semantic.DeepEqual(parent.ParentRef, expected.ParentRefs[0]) {
			continue
		}
		accepted := currentRouteCondition(
			parent.Conditions,
			string(gatewayv1.RouteConditionAccepted),
			current.Generation,
		)
		resolved := currentRouteCondition(
			parent.Conditions,
			string(gatewayv1.RouteConditionResolvedRefs),
			current.Generation,
		)
		if accepted && resolved {
			observation.Ready = true
			return nil
		}
	}
	return nil
}

func currentRouteCondition(conditions []metav1.Condition, conditionType string, generation int64) bool {
	condition := meta.FindStatusCondition(conditions, conditionType)
	if condition == nil {
		return false
	}
	return condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == generation
}
