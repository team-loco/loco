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

func (r *ApplicationReconciler) publicRouteAccepted(
	ctx context.Context,
	app *loco.Application,
	routeName string,
	desired *gatewayac.HTTPRouteSpecApplyConfiguration,
) (bool, error) {
	current := &gatewayv1.HTTPRoute{}
	key := client.ObjectKey{Namespace: r.environmentNamespace(app), Name: routeName}
	if err := r.APIReader.Get(ctx, key, current); err != nil {
		return false, fmt.Errorf("read public route %s: %w", key, err)
	}
	encoded, err := json.Marshal(desired)
	if err != nil {
		return false, fmt.Errorf("encode desired public route: %w", err)
	}
	var expected gatewayv1.HTTPRouteSpec
	if err := json.Unmarshal(encoded, &expected); err != nil {
		return false, fmt.Errorf("decode desired public route: %w", err)
	}
	if !equality.Semantic.DeepEqual(current.Spec, expected) {
		return false, nil
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
			return true, nil
		}
	}
	return false, nil
}

func currentRouteCondition(conditions []metav1.Condition, conditionType string, generation int64) bool {
	condition := meta.FindStatusCondition(conditions, conditionType)
	if condition == nil {
		return false
	}
	return condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == generation
}
