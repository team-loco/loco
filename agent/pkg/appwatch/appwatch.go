package appwatch

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/agent/pkg/kube"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

type Watcher struct {
	reader    client.Reader
	secrets   client.Reader
	namespace string
	feed      *kube.Feed[*agentv1.PlacementStatus]
	cache     *kube.Cache
}

func Start(ctx context.Context, cfg *rest.Config, namespace string) (*Watcher, error) {
	scheme, err := kube.NewScheme()
	if err != nil {
		return nil, err
	}
	secrets, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Secret client: %w", err)
	}
	w := newWatcher(nil, secrets, namespace)
	c, err := kube.StartCache(ctx, cfg, scheme, namespace, &locoControllerV1.Application{}, w.observe, w.forget)
	if err != nil {
		return nil, err
	}
	// observe and forget never read reader, so it can be set after the cache starts.
	w.reader, w.cache = c, c
	return w, nil
}

// Wait blocks until ctx ends or the Application watch stops for good, and returns why.
func (w *Watcher) Wait(ctx context.Context) error {
	return w.cache.Wait(ctx)
}

func newWatcher(reader, secrets client.Reader, namespace string) *Watcher {
	return &Watcher{
		reader:    reader,
		secrets:   secrets,
		namespace: namespace,
		feed:      kube.NewFeed[*agentv1.PlacementStatus](),
	}
}

// WithInventory calls fn with the inventory while no status is forwarded, so the API never
// receives an inventory older than a status it already has.
func (w *Watcher) WithInventory(ctx context.Context, fn func(*agentv1.Inventory) error) error {
	return w.feed.Freeze(func() error {
		inventory, err := w.inventory(ctx)
		if err != nil {
			return err
		}
		return fn(inventory)
	})
}

// Attach replays every known placement status to sink and forwards later changes to it.
func (w *Watcher) Attach(sink func(*agentv1.PlacementStatus)) func() {
	return w.feed.Attach(sink)
}

// inventory lists the placement revision of every Application. An Application that references a
// staging Secret not yet labeled with the referenced revision is reported with EnvSecretPending,
// so the API resends an Apply that stopped between its two writes instead of recording it as
// applied, and can still advance past a revision the cluster holds ahead of the database.
func (w *Watcher) inventory(ctx context.Context) (*agentv1.Inventory, error) {
	var apps locoControllerV1.ApplicationList
	// Inventory only reads placement and Secret references; never mutate these shared cache objects.
	if err := w.reader.List(ctx, &apps, client.InNamespace(w.namespace), client.UnsafeDisableDeepCopy); err != nil {
		return nil, fmt.Errorf("failed to list Applications: %w", err)
	}
	staged, err := w.stagedRevisions(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]*agentv1.InventoryEntry, 0, len(apps.Items))
	for i := range apps.Items {
		app := &apps.Items[i]
		placementID, revision, ok := app.Placement()
		if !ok {
			continue
		}
		entries = append(entries, &agentv1.InventoryEntry{
			PlacementId:      placementID,
			Revision:         revision,
			EnvSecretPending: !envSecretStaged(app, staged),
		})
	}
	return &agentv1.Inventory{Entries: entries}, nil
}

func (w *Watcher) stagedRevisions(ctx context.Context) (map[string]int64, error) {
	var secrets corev1.SecretList
	hasPlacement := client.HasLabels{locoControllerV1.KeyPlacementID}
	if err := w.secrets.List(ctx, &secrets, client.InNamespace(w.namespace), hasPlacement); err != nil {
		return nil, fmt.Errorf("failed to list env Secrets: %w", err)
	}
	revisions := make(map[string]int64, len(secrets.Items))
	for i := range secrets.Items {
		revision, ok := locoControllerV1.EnvSecretRevision(secrets.Items[i].Labels)
		if ok {
			revisions[secrets.Items[i].Name] = revision
		}
	}
	return revisions, nil
}

func envSecretStaged(app *locoControllerV1.Application, staged map[string]int64) bool {
	spec := app.Spec.ServiceSpec
	if spec == nil || spec.Deployment == nil || spec.Deployment.EnvSecretRef == nil {
		return true
	}
	ref := spec.Deployment.EnvSecretRef
	revision, ok := staged[ref.Name]
	return ok && revision == ref.Revision
}

func (w *Watcher) observe(app *locoControllerV1.Application) {
	placementID, _, ok := app.Placement()
	if !ok {
		return
	}
	status := &agentv1.PlacementStatus{
		PlacementId:      placementID,
		DeploymentId:     app.Status.ObservedDeploymentID,
		ObservedRevision: app.Status.ObservedPlacementRevision,
		ReadyReplicas:    app.Status.ReadyReplicas,
	}
	if rollout := app.Status.Rollout; rollout != nil {
		status.DesiredReplicas = rollout.DesiredReplicas
		status.Available = rollout.Available
		status.Healthy = rollout.Healthy
		deployed := rollout.DeployedRevision
		status.Deployed = deployed != 0 && deployed == app.Status.ObservedPlacementRevision
		if rollout.HealthyAt != nil {
			status.HealthyAt = timestamppb.New(rollout.HealthyAt.Time)
		}
		if stopped := rollout.Stopped; stopped != nil {
			status.Stopped = &agentv1.StoppedRevision{
				PlacementRevision: stopped.PlacementRevision,
				Reason:            stopReason(stopped.Reason),
				ReplicaName:       stopped.Replica,
				Restarts:          stopped.Restarts,
				At:                timestamppb.New(stopped.At.Time),
				Rollout:           stopped.Rollout,
			}
			if stopped.LastRestart != nil {
				status.Stopped.LastRestart = replicaRestart(*stopped.LastRestart)
			}
		}
		for _, replica := range rollout.Replicas {
			report := &deploymentv1.Replica{
				Uid:               replica.UID,
				Name:              replica.Name,
				Node:              replica.Node,
				PlacementRevision: replica.PlacementRevision,
				DeploymentId:      replica.DeploymentID,
				Ready:             replica.Ready,
				State:             string(replica.State),
				Restarts:          replica.Restarts,
				Detail:            replica.Detail,
			}
			if policy := replica.RestartPolicy; policy != nil {
				report.FailurePolicy = &deploymentv1.FailurePolicy{
					RestartPolicy: restartPolicy(policy.Mode), MaxRestarts: policy.MaxRestarts,
				}
			}
			if replica.StartedAt != nil {
				report.StartedAt = timestamppb.New(replica.StartedAt.Time)
			}
			if exit := replica.LastExit; exit != nil {
				report.LastExit = &deploymentv1.ReplicaExit{Reason: exit.Reason, ExitCode: exit.ExitCode}
				if !exit.FinishedAt.IsZero() {
					report.LastExit.FinishedAt = timestamppb.New(exit.FinishedAt.Time)
				}
			}
			if replica.StateSince != nil {
				report.StateSince = timestamppb.New(replica.StateSince.Time)
			}
			if replica.StateDeadline != nil {
				report.StateDeadline = timestamppb.New(replica.StateDeadline.Time)
			}
			for _, at := range replica.RestartsAt {
				report.RestartsAt = append(report.RestartsAt, timestamppb.New(at.Time))
			}
			if replica.LastRestart != nil {
				report.LastRestart = replicaRestart(*replica.LastRestart)
			}
			status.Replicas = append(status.Replicas, report)
		}
		if problem := rollout.Problem; problem != nil {
			status.Problem = &agentv1.ProblemReport{
				Reason:      string(problem.Reason),
				ReplicaName: problem.Replica,
				Detail:      problem.Detail,
			}
			if problem.Since != nil {
				status.Problem.Since = timestamppb.New(problem.Since.Time)
			}
		}
	}
	w.feed.Publish(placementID, status)
}

// stopReason maps the reason the controller stopped a revision to its proto value.
func stopReason(reason locoControllerV1.ProblemReason) agentv1.StopReason {
	switch reason {
	case locoControllerV1.ProblemRestartsExhausted:
		return agentv1.StopReason_STOP_REASON_RESTARTS_EXHAUSTED
	case locoControllerV1.ProblemConfigError,
		locoControllerV1.ProblemImagePullFailed:
		return agentv1.StopReason_STOP_REASON_STARTUP_FAILED
	case locoControllerV1.ProblemDeployTimeout:
		return agentv1.StopReason_STOP_REASON_DEPLOY_TIMEOUT
	default:
		return agentv1.StopReason_STOP_REASON_UNSPECIFIED
	}
}

// restartPolicy maps a restart mode to its proto value, as api/pkg/servicedefaults does; update both together.
func restartPolicy(mode locoControllerV1.RestartMode) deploymentv1.RestartPolicy {
	switch mode {
	case locoControllerV1.RestartOnFailure:
		return deploymentv1.RestartPolicy_RESTART_POLICY_ON_FAILURE
	case locoControllerV1.RestartAlways:
		return deploymentv1.RestartPolicy_RESTART_POLICY_ALWAYS
	case locoControllerV1.RestartNever:
		return deploymentv1.RestartPolicy_RESTART_POLICY_NEVER
	default:
		return deploymentv1.RestartPolicy_RESTART_POLICY_UNSPECIFIED
	}
}

func replicaRestart(restart locoControllerV1.ReplicaRestart) *deploymentv1.ReplicaRestart {
	return &deploymentv1.ReplicaRestart{
		At:       timestamppb.New(restart.At.Time),
		Cause:    string(restart.Cause),
		ExitCode: restart.ExitCode,
	}
}

func (w *Watcher) forget(app *locoControllerV1.Application) {
	placementID, _, ok := app.Placement()
	if !ok {
		return
	}
	w.feed.Forget(placementID)
}
