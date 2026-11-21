package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// annotationTemplateHash identifies a pod template apart from the revisions it carries.
const annotationTemplateHash = "loco.io/template-hash"

// revisionAnnotations change with every placement revision without changing what the pods run.
var revisionAnnotations = []string{loco.KeyPlacementRevision, locolabels.AnnotationEnvSecretRevision}

// hashTemplate stamps the template with the hash of everything but its revision annotations.
func hashTemplate(template *corev1ac.PodTemplateSpecApplyConfiguration) error {
	meta := *template.ObjectMetaApplyConfiguration
	meta.Annotations = maps.Clone(template.Annotations)
	delete(meta.Annotations, annotationTemplateHash)
	for _, key := range revisionAnnotations {
		delete(meta.Annotations, key)
	}
	unrevised := *template
	unrevised.ObjectMetaApplyConfiguration = &meta
	encoded, err := json.Marshal(unrevised)
	if err != nil {
		return fmt.Errorf("encode pod template: %w", err)
	}
	sum := sha256.Sum256(encoded)
	template.WithAnnotations(map[string]string{annotationTemplateHash: hex.EncodeToString(sum[:16])})
	return nil
}

// reviveReplicaSet relabels the newest ReplicaSet that runs the desired template, and its pods,
// with the template's revisions when the Deployment runs another template. The Deployment then
// adopts that ReplicaSet as its current one and its pods keep running. A restart keeps the
// Deployment's template, so it still replaces the pods.
func (r *ApplicationReconciler) reviveReplicaSet(
	ctx context.Context,
	namespace, name string,
	template *corev1ac.PodTemplateSpecApplyConfiguration,
) error {
	hash := template.Annotations[annotationTemplateHash]
	key := client.ObjectKey{Namespace: namespace, Name: name}
	var live appsv1.Deployment
	err := r.Get(ctx, key, &live)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	if maps.Equal(live.Spec.Template.Annotations, template.Annotations) {
		return nil
	}
	// A cached Deployment that predates the last apply would revive the pods a restart replaces.
	if err := r.APIReader.Get(ctx, key, &live); err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	if live.Spec.Template.Annotations[annotationTemplateHash] == hash {
		return nil
	}
	var sets appsv1.ReplicaSetList
	if err := r.APIReader.List(ctx, &sets, client.InNamespace(namespace),
		client.MatchingLabels{labelApp: name}); err != nil {
		return fmt.Errorf("list replica sets of %s/%s: %w", namespace, name, err)
	}
	var match *appsv1.ReplicaSet
	for i := range sets.Items {
		set := &sets.Items[i]
		if set.Spec.Template.Annotations[annotationTemplateHash] == hash &&
			(match == nil || deploymentRevision(set) > deploymentRevision(match)) {
			match = set
		}
	}
	if match == nil {
		return nil
	}
	patch := client.MergeFrom(match.DeepCopy())
	if reviseAnnotations(&match.Spec.Template.ObjectMeta, template.Annotations) {
		if err := r.Patch(ctx, match, patch); err != nil {
			return fmt.Errorf("revise replica set %s/%s: %w", namespace, match.Name, err)
		}
	}
	return r.revivePods(ctx, match, template.Annotations)
}

func (r *ApplicationReconciler) revivePods(
	ctx context.Context,
	set *appsv1.ReplicaSet,
	annotations map[string]string,
) error {
	var pods corev1.PodList
	if err := r.APIReader.List(ctx, &pods, client.InNamespace(set.Namespace),
		client.MatchingLabels(set.Spec.Selector.MatchLabels)); err != nil {
		return fmt.Errorf("list pods of replica set %s/%s: %w", set.Namespace, set.Name, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !metav1.IsControlledBy(pod, set) || pod.DeletionTimestamp != nil {
			continue
		}
		patch := client.MergeFrom(pod.DeepCopy())
		if !reviseAnnotations(&pod.ObjectMeta, annotations) {
			continue
		}
		if err := r.Patch(ctx, pod, patch); err != nil {
			return fmt.Errorf("revise pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

// reviseAnnotations sets the revision annotations of meta to those in desired and reports a change.
func reviseAnnotations(meta *metav1.ObjectMeta, desired map[string]string) bool {
	changed := false
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	for _, key := range revisionAnnotations {
		value, ok := desired[key]
		current, had := meta.Annotations[key]
		switch {
		case ok && (!had || current != value):
			meta.Annotations[key] = value
			changed = true
		case !ok && had:
			delete(meta.Annotations, key)
			changed = true
		}
	}
	return changed
}
