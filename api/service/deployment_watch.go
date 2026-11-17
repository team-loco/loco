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
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	deploymentActivityBatchSize = 200
	watchDeploymentMaxDuration  = 30 * time.Minute
)

func (s *DeploymentServer) WatchDeployment(
	ctx context.Context,
	req *deploymentv1.WatchDeploymentRequest,
	stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream,
) (err error) {
	principal, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	if !s.acquireWatch(principal.Entity) {
		return errDeploymentWatchLimit
	}
	defer s.releaseWatch(principal.Entity)
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
		deployment, activity, readErr := s.readDeployment(ctx, id, cursor)
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
		failed := deployment.GetStatus() == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED
		if isTerminalDeploymentStatus(deployment.GetStatus()) || failed && !deployment.GetIsActive() {
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

func (s *DeploymentServer) ListDeploymentActivity(
	ctx context.Context,
	req *deploymentv1.ListDeploymentActivityRequest,
) (*deploymentv1.ListDeploymentActivityResponse, error) {
	id := uuid.MustParse(req.GetDeploymentId())
	if err := s.authorizeDeploymentActivity(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListDeploymentActivity(
		ctx,
		genDb.ListDeploymentActivityParams{
			DeploymentID: id,
			AfterSeq:     req.GetAfterSeq(),
			PageSize:     normalizePageSize(req.GetPageSize()),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list deployment activity: %w", err)
	}
	activity := activityToProto(rows)
	cursor := req.GetAfterSeq()
	if len(activity) > 0 {
		cursor = activity[len(activity)-1].GetSeq()
	}
	return &deploymentv1.ListDeploymentActivityResponse{Activity: activity, NextSeq: cursor}, nil
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
			},
		)
	}
	return activity
}

func isTerminalDeploymentStatus(status deploymentv1.DeploymentStatus) bool {
	return status == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED ||
		status == deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED
}

func setDeploymentRollout(deployment *deploymentv1.Deployment, data, observed []byte) error {
	var snapshot rolloutSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode rollout: %w", err)
	}
	if report := snapshot.Problem; report != nil {
		message, ok := loco.ProblemMessage(loco.ProblemReason(report.GetReason()))
		if !ok {
			return fmt.Errorf("unknown stored problem reason %q", report.GetReason())
		}
		deployment.Problem = &deploymentv1.Problem{
			Reason:      report.GetReason(),
			Message:     message,
			Detail:      report.GetDetail(),
			ReplicaName: report.GetReplicaName(),
			Since:       report.GetSince(),
		}
	}
	if deployment.GetIsActive() {
		snapshot = rolloutSnapshot{}
		if err := json.Unmarshal(observed, &snapshot); err != nil {
			return fmt.Errorf("decode observed rollout: %w", err)
		}
	}
	for _, r := range snapshot.Replicas {
		deployment.Replicas = append(
			deployment.Replicas,
			&deploymentv1.Replica{
				Uid:               r.GetUid(),
				Name:              r.GetName(),
				Node:              r.GetNode(),
				PlacementRevision: r.GetPlacementRevision(),
				DeploymentId:      r.GetDeploymentId(),
				Ready:             r.GetReady(),
				State:             r.GetState(),
				StateSince:        r.GetStateSince(),
				StateDeadline:     r.GetStateDeadline(),
				Restarts:          r.GetRestarts(),
				ExitCode:          r.ExitCode,
				Detail:            r.GetDetail(),
			},
		)
	}

	return nil
}
