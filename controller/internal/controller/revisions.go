package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// replicaSetRevisions records the revision on ReplicaSets running the applied template and returns all by name.
func (r *ApplicationReconciler) replicaSetRevisions(
	ctx context.Context,
	app *loco.Application,
	dep *appsv1ac.DeploymentApplyConfiguration,
) (map[string]int64, error) {
	sets, err := r.replicaSets(ctx, *dep.Namespace, *dep.Name)
	if err != nil {
		return nil, err
	}
	// The applied Deployment holds the template as the API server defaulted it, as its ReplicaSets do.
	raw, err := json.Marshal(dep.Spec.Template)
	if err != nil {
		return nil, fmt.Errorf("encode the template of deployment %s/%s: %w", *dep.Namespace, *dep.Name, err)
	}
	var template corev1.PodTemplateSpec
	if err = json.Unmarshal(raw, &template); err != nil {
		return nil, fmt.Errorf("decode the template of deployment %s/%s: %w", *dep.Namespace, *dep.Name, err)
	}
	revision, placed := app.PlacementRevision()
	revisions := make(map[string]int64, len(sets))
	for i := range sets {
		set := &sets[i]
		setRevision := replicaSetRevision(set)
		if placed && setRevision < revision && runsTemplate(set, &template) {
			patch := client.MergeFrom(set.DeepCopy())
			metav1.SetMetaDataAnnotation(&set.ObjectMeta, loco.KeyPlacementRevision, strconv.FormatInt(revision, 10))
			if err = r.Patch(ctx, set, patch); err != nil {
				return nil, fmt.Errorf("record the revision of replica set %s/%s: %w", set.Namespace, set.Name, err)
			}
			setRevision = revision
		}
		revisions[set.Name] = setRevision
	}
	return revisions, nil
}

// podRevision is the placement revision of the ReplicaSet that owns the pod, 0 when none is recorded.
func podRevision(pod *corev1.Pod, revisions map[string]int64) int64 {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return 0
	}
	return revisions[owner.Name]
}

func (r *ApplicationReconciler) replicaSets(ctx context.Context, namespace, name string) ([]appsv1.ReplicaSet, error) {
	var sets appsv1.ReplicaSetList
	if err := r.APIReader.List(ctx, &sets, client.InNamespace(namespace),
		client.MatchingLabels{labelApp: name}); err != nil {
		return nil, fmt.Errorf("list replica sets of %s/%s: %w", namespace, name, err)
	}
	return sets.Items, nil
}

// replicaSetRevision is the placement revision recorded on the ReplicaSet, 0 when none is.
func replicaSetRevision(set *appsv1.ReplicaSet) int64 {
	revision, err := strconv.ParseInt(set.Annotations[loco.KeyPlacementRevision], 10, 64)
	if err != nil {
		return 0
	}
	return revision
}

// runsTemplate matches a ReplicaSet to a template as the Deployment controller finds its current ReplicaSet.
func runsTemplate(set *appsv1.ReplicaSet, template *corev1.PodTemplateSpec) bool {
	own := set.Spec.Template.DeepCopy()
	delete(own.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	return equality.Semantic.DeepEqual(own, template)
}
