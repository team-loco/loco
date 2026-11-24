package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracing"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	deploymentActivityBatchSize = 200
	watchDeploymentMaxDuration  = 30 * time.Minute
	deploymentWatchPollSpan     = "deployment.watch.poll"
)

func (s *DeploymentServer) WatchDeployment(
	ctx context.Context,
	req *deploymentv1.WatchDeploymentRequest,
	stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream,
) (err error) {
	id := uuid.MustParse(req.GetDeploymentId())
	if err = s.authorizeDeploymentActivity(ctx, id); err != nil {
		return err
	}
	ctx, stop, err := auth.GuardStream(ctx, func(check context.Context) error {
		return s.authorizeDeploymentActivity(check, id)
	})
	if err != nil {
		return err
	}
	defer func() {
		if authErr := stop(); authErr != nil {
			err = authErr
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, watchDeploymentMaxDuration)
	defer cancel()
	listener := s.notifier.Listen(id)
	defer listener.Close()
	ticker := time.NewTicker(s.notifier.PollInterval())
	defer ticker.Stop()
	cursor := req.GetAfterSeq()
	var previous *deploymentv1.Deployment
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		pollCtx, span := tracing.StartLinked(ctx, deploymentWatchPollSpan)
		deployment, activity, readErr := s.readDeployment(pollCtx, id, cursor)
		tracing.End(span, readErr)
		if readErr != nil {
			return readErr
		}
		hasMore := len(activity) > deploymentActivityBatchSize
		if hasMore {
			activity = activity[:deploymentActivityBatchSize]
		}
		if !proto.Equal(previous, deployment) || len(activity) > 0 {
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = stream.Send(
				&deploymentv1.WatchDeploymentResponse{
					Deployment:      deployment,
					Activity:        activity,
					HasMoreActivity: hasMore,
				},
			); err != nil {
				return err
			}
			previous = deployment
			if len(activity) > 0 {
				cursor = activity[len(activity)-1].GetSeq()
			}
		}
		if hasMore {
			continue
		}
		if isTerminalDeploymentStatus(deployment.GetStatus()) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-listener.Wake():
		case <-ticker.C:
		}
	}
}

func (s *DeploymentServer) authorizeDeploymentActivity(ctx context.Context, id uuid.UUID) error {
	resource, err := s.queries.GetDeploymentResourceID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeploymentNotFound
	}
	if err != nil {
		return fmt.Errorf("get deployment resource: %w", err)
	}
	return authorizeVisible(ctx, s.authz, actions.New(actions.StreamDeployment, resource), ErrDeploymentNotFound)
}

func (s *DeploymentServer) readDeployment(
	ctx context.Context,
	id uuid.UUID,
	cursor int64,
) (*deploymentv1.Deployment, []*deploymentv1.Activity, error) {
	var deployment *deploymentv1.Deployment
	var activity []*deploymentv1.Activity
	err := pgx.BeginTxFunc(
		ctx,
		s.db,
		pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			q := genDb.New(tx)
			row, err := q.GetDeploymentView(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDeploymentNotFound
			}
			if err != nil {
				return fmt.Errorf("get deployment: %w", err)
			}
			deployment, err = deploymentToProto(row)
			if err != nil {
				return err
			}
			rows, err := q.ListDeploymentActivity(
				ctx,
				genDb.ListDeploymentActivityParams{
					DeploymentID: id,
					AfterSeq:     cursor,
					PageSize:     deploymentActivityBatchSize + 1,
				},
			)
			if err != nil {
				return fmt.Errorf("read deployment activity: %w", err)
			}
			activity = activityToProto(rows)
			return nil
		},
	)
	if err != nil {
		return nil, nil, err
	}
	return deployment, activity, nil
}

func activityToProto(rows []genDb.DeploymentActivity) []*deploymentv1.Activity {
	activity := make([]*deploymentv1.Activity, 0, len(rows))
	for _, row := range rows {
		activity = append(
			activity,
			&deploymentv1.Activity{
				Seq:         row.Seq,
				Revision:    row.Revision,
				At:          timestamppb.New(row.At),
				Severity:    row.Severity,
				ReplicaName: row.ReplicaName,
				Message:     row.Message,
				Region:      row.Region,
			},
		)
	}
	return activity
}

// isTerminalDeploymentStatus reports a status a deployment never leaves; a failed one can still recover.
func isTerminalDeploymentStatus(status deploymentv1.DeploymentStatus) bool {
	return status == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED ||
		status == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED
}

// setPlacementRollout fills a placement's problem from its latest rollout and its replicas: the
// cluster's latest report while the placement is live, the last snapshot otherwise.
func setPlacementRollout(
	placement *deploymentv1.DeploymentPlacement,
	buildID string,
	data, observed []byte,
) error {
	var snapshot rolloutSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode rollout: %w", err)
	}
	if report := snapshot.Problem; report != nil {
		message, ok := loco.ProblemMessage(loco.ProblemReason(report.GetReason()))
		if !ok {
			return fmt.Errorf("unknown stored problem reason %q", report.GetReason())
		}
		placement.Problem = &deploymentv1.Problem{
			Reason:      report.GetReason(),
			Message:     message,
			Detail:      report.GetDetail(),
			ReplicaName: report.GetReplicaName(),
			Since:       report.GetSince(),
			LogsQuery:   problemLogsQuery(report, buildID),
		}
	}
	if placement.GetLive() {
		snapshot.Replicas = nil
		if err := json.Unmarshal(observed, &snapshot.Replicas); err != nil {
			return fmt.Errorf("decode observed rollout: %w", err)
		}
	}
	placement.Replicas = snapshot.Replicas
	return nil
}

// problemLogsQuery selects the log lines that explain a problem.
func problemLogsQuery(report *agentv1.ProblemReport, buildID string) string {
	switch {
	case report.GetReason() == string(loco.ProblemBuildFailed) && buildID != "":
		return "@build:" + buildID
	case report.GetReplicaName() != "":
		return "@replica:" + report.GetReplicaName()
	default:
		return ""
	}
}
