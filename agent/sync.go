package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/team-loco/loco/agent/pkg/applier"
	"github.com/team-loco/loco/agent/pkg/reconciler"
	"github.com/team-loco/loco/agent/pkg/rpcretry"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

func (a *Agent) runSync(ctx context.Context) error {
	return a.reconnectLoop(ctx, a.syncLoop)
}

func (a *Agent) reconnectLoop(ctx context.Context, attempt func(context.Context) error) error {
	backoff := newReconnectBackoff(a.cfg)
	for {
		started := time.Now()
		err := attempt(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !rpcretry.Retryable(err) {
			return fmt.Errorf("sync stream: %w", err)
		}

		elapsed := time.Since(started)
		if elapsed >= a.cfg.HealthyStreamDuration {
			backoff = newReconnectBackoff(a.cfg)
		}
		delay := backoff.Step()
		slog.WarnContext(ctx, "sync stream error, reconnecting", "error", err, "delay", delay)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type syncSession struct {
	stream   agentv1connect.AgentServiceSyncClientStream
	outbound chan *agentv1.SyncRequest
	done     <-chan struct{}
}

func (ss *syncSession) enqueue(msg *agentv1.SyncRequest) {
	select {
	case ss.outbound <- msg:
	case <-ss.done:
	}
}

func (ss *syncSession) sendLoop(ctx context.Context, sendErr chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ss.outbound:
			if err := ss.stream.Send(msg); err != nil {
				sendErr <- streamError(ss.stream, err)
				return
			}
		}
	}
}

func (a *Agent) syncLoop(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := a.client.Sync(streamCtx)
	if err != nil {
		return err
	}

	session := &syncSession{
		stream:   stream,
		outbound: make(chan *agentv1.SyncRequest, a.cfg.SyncOutboundBuffer),
		done:     streamCtx.Done(),
	}
	reconcilerConfig := reconciler.Config{
		Workers:        a.cfg.ReconcileWorkers,
		RetryBaseDelay: a.cfg.ReconcileRetryBaseDelay,
		RetryMaxDelay:  a.cfg.ReconcileRetryMaxDelay,
	}
	rec := reconciler.New(a.applier, func(applied *agentv1.Applied) {
		session.enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{Applied: applied}})
	}, reconcilerConfig)

	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
		closeStream(ctx, stream)
	}()

	err = a.sendInventory(streamCtx, func(inventory *agentv1.Inventory) error {
		request := inventoryRequest(inventory)
		if sendErr := stream.Send(request); sendErr != nil {
			openErr := streamError(stream, sendErr)
			return fmt.Errorf("open sync stream: %w", openErr)
		}
		slog.InfoContext(ctx, "sync stream connected",
			"inventory", len(inventory.GetEntries()),
			"builds", len(inventory.GetBuilds()),
		)
		return nil
	})
	if err != nil {
		return err
	}

	buildOps := make(chan *agentv1.SyncResponse, a.cfg.BuildQueueSize)
	sendErr := make(chan error, 1)
	workers.Go(func() { rec.Run(streamCtx) })
	workers.Go(func() { session.sendLoop(streamCtx, sendErr) })
	workers.Go(func() { a.reportInventory(streamCtx, session) })
	workers.Go(func() { a.sendHeartbeats(streamCtx, session.enqueue) })
	workers.Go(func() { a.runBuildOps(streamCtx, buildOps, session.enqueue) })
	detach := a.watcher.Attach(func(status *agentv1.PlacementStatus) {
		session.enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Status{Status: status}})
	})
	defer detach()
	detachBuilds := a.builds.Attach(func(status *agentv1.BuildStatus) {
		session.enqueue(buildStatusRequest(status))
	})
	defer detachBuilds()

	received := make(chan *agentv1.SyncResponse)
	recvErr := make(chan error, 1)
	workers.Go(func() { receiveSync(streamCtx, stream, received, recvErr) })

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sendErr:
			return fmt.Errorf("send sync message: %w", err)
		case err := <-recvErr:
			return fmt.Errorf("receive sync message: %w", err)
		case msg := <-received:
			if workspaceID := msg.GetDeleteWorkspace(); workspaceID != "" {
				if err := a.applier.DeleteWorkspace(streamCtx, workspaceID); err != nil {
					return fmt.Errorf("delete workspace: %w", err)
				}
				continue
			}
			if isBuildMessage(msg) {
				select {
				case buildOps <- msg:
				case <-streamCtx.Done():
					return streamCtx.Err()
				}
				continue
			}
			submitPlacement(streamCtx, rec, msg)
		}
	}
}

func receiveSync(
	ctx context.Context,
	stream agentv1connect.AgentServiceSyncClientStream,
	received chan<- *agentv1.SyncResponse,
	recvErr chan<- error,
) {
	for {
		msg, err := stream.Receive()
		if err != nil {
			recvErr <- err
			return
		}
		select {
		case received <- msg:
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) reportInventory(ctx context.Context, session *syncSession) {
	ticker := time.NewTicker(a.cfg.InventoryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := a.sendInventory(ctx, func(inventory *agentv1.Inventory) error {
				request := inventoryRequest(inventory)
				session.enqueue(request)
				return nil
			})
			if err != nil {
				slog.ErrorContext(ctx, "failed to build inventory", "error", err)
			}
		}
	}
}

func (a *Agent) sendInventory(ctx context.Context, send func(*agentv1.Inventory) error) error {
	workspaceIDs, err := a.applier.WorkspaceIDs(ctx)
	if err != nil {
		return err
	}
	return a.builds.WithInventory(ctx, func(builds []*agentv1.InventoryBuild) error {
		return a.watcher.WithInventory(ctx, func(inventory *agentv1.Inventory) error {
			inventory.Builds = builds
			inventory.WorkspaceIds = workspaceIDs
			return send(inventory)
		})
	})
}

func buildStatusRequest(status *agentv1.BuildStatus) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_BuildStatus{BuildStatus: status}}
}

func isBuildMessage(msg *agentv1.SyncResponse) bool {
	switch msg.GetMessage().(type) {
	case *agentv1.SyncResponse_StartBuild:
		return true
	case *agentv1.SyncResponse_CancelBuild:
		return true
	default:
		return false
	}
}

func (a *Agent) runBuildOps(
	ctx context.Context,
	ops <-chan *agentv1.SyncResponse,
	enqueue func(*agentv1.SyncRequest),
) {
	report := func(status *agentv1.BuildStatus) {
		request := buildStatusRequest(status)
		enqueue(request)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ops:
			a.builds.Handle(ctx, msg, report)
		}
	}
}

func inventoryRequest(inventory *agentv1.Inventory) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{Inventory: inventory}}
}

func submitPlacement(ctx context.Context, rec *reconciler.Reconciler, msg *agentv1.SyncResponse) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_Apply:
		slog.InfoContext(ctx, "received placement",
			"placement_id", m.Apply.GetPlacementId(),
			"revision", m.Apply.GetRevision(),
			"resource_id", m.Apply.GetResourceId(),
		)
		rec.Submit(reconciler.Work{Placement: applier.Placement{
			ID:          m.Apply.GetPlacementId(),
			Revision:    m.Apply.GetRevision(),
			ResourceID:  m.Apply.GetResourceId(),
			Application: m.Apply.GetApplication(),
			EnvSecret:   envSecretOf(m.Apply.GetEnvSecret()),
		}})
	case *agentv1.SyncResponse_Delete:
		slog.InfoContext(ctx, "received placement deletion",
			"placement_id", m.Delete.GetPlacementId(),
			"revision", m.Delete.GetRevision(),
			"resource_id", m.Delete.GetResourceId(),
		)
		rec.Submit(reconciler.Work{
			Placement: applier.Placement{
				ID:         m.Delete.GetPlacementId(),
				Revision:   m.Delete.GetRevision(),
				ResourceID: m.Delete.GetResourceId(),
			},
			Delete: true,
		})
	default:
		slog.WarnContext(ctx, "ignoring empty sync message")
	}
}

func envSecretOf(secret *agentv1.EnvSecret) *applier.EnvSecret {
	if secret == nil {
		return nil
	}
	return &applier.EnvSecret{Revision: secret.GetRevision(), Data: secret.GetData()}
}

func closeStream(ctx context.Context, stream agentv1connect.AgentServiceSyncClientStream) {
	if err := stream.CloseSend(); err != nil {
		slog.DebugContext(ctx, "close sync stream request", "error", err)
	}
	if err := stream.Close(); err != nil {
		slog.DebugContext(ctx, "close sync stream response", "error", err)
	}
}

func streamError(stream agentv1connect.AgentServiceSyncClientStream, sendErr error) error {
	if !errors.Is(sendErr, io.EOF) {
		return sendErr
	}
	_, recvErr := stream.Receive()
	if recvErr == nil || errors.Is(recvErr, io.EOF) {
		return sendErr
	}
	return recvErr
}
