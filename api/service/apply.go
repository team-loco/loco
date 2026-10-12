package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errPlanHasErrors          = errors.New("the plan has errors")
	errRevisionMoved          = errors.New("the environment changed since the plan was computed")
	errConfirmationRequired   = errors.New("the plan has operations that need a confirmation")
	errBuildServiceUnknown    = errors.New("the file does not run the service in this environment")
	errBuildServiceHasImage   = errors.New("the service runs a public image and takes no build")
	errBuildServiceNotCreated = errors.New("the service has no resource yet; apply the file before building it")
	errImagesMoved            = errors.New("an image reference resolves to another digest than the plan showed")
	errProvisionWithBuilds    = errors.New("an apply provisions services or deploys builds, not both")
	errProvisionNotPlanned    = errors.New("the plan does not create the service")
)

// Apply re-plans the file against the environment and performs the plan's operations in one
// transaction. It needs workspace write, and resource admin on every service the plan deletes.
func (s *PlanServer) Apply(
	ctx context.Context,
	req *planv1.ApplyRequest,
) (*planv1.ApplyResponse, error) {
	planned, err := s.loadPlan(ctx, req.GetFile(), req.GetEnvironmentId(), actions.Apply, req.GetProvision())
	if err != nil {
		return nil, err
	}
	plan := planned.plan
	if len(plan.Errors) > 0 {
		return nil, applyRefused(errPlanHasErrors, &planv1.ApplyRefusal{Errors: planErrorsToProto(plan.Errors)})
	}
	if planned.env.Revision != req.GetRevision() {
		return nil, applyRefused(errRevisionMoved, &planv1.ApplyRefusal{Revision: planned.env.Revision})
	}
	if !maps.Equal(planned.images, req.GetImages()) {
		return nil, applyRefused(errImagesMoved, &planv1.ApplyRefusal{Images: planned.images})
	}
	operations, err := selectOperations(plan.Operations, req.GetProvision(), req.GetBuilds())
	if err != nil {
		return nil, err
	}
	unconfirmed := unconfirmedOperations(operations, req.GetConfirmDestructive(), req.GetConfirmImport())
	if len(unconfirmed) > 0 {
		refusal := &planv1.ApplyRefusal{Unconfirmed: planOperationsToProto(unconfirmed)}
		return nil, applyRefused(errConfirmationRequired, refusal)
	}
	if deleteErr := s.checkDeletes(ctx, planned, operations); deleteErr != nil {
		return nil, deleteErr
	}
	builds, err := s.resolveBuilds(ctx, planned, req.GetBuilds())
	if err != nil {
		return nil, err
	}

	organizationID, err := s.queries.GetOrganizationIDByWorkspaceID(ctx, planned.env.WorkspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get workspace organization", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	a := &applier{
		env:             planned.env,
		organizationID:  organizationID,
		partial:         planned.file.Partial,
		live:            planned.live,
		desired:         plan.Desired,
		builds:          builds,
		deployed:        map[string]bool{},
		platformDomains: planned.platformDomains,
		defaults:        s.defaults,
		provisioning:    len(req.GetProvision()) > 0,
		silentAfter:     s.silentAfter,
	}
	var revision int64
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		revisions, lockErr := lockWorkspaceEnvironments(ctx, qtx, planned.env.WorkspaceID)
		if lockErr != nil {
			return lockErr
		}
		if revisions[planned.env.ID] != req.GetRevision() {
			return errRevisionMoved
		}
		if _, lockErr := lockResources(
			ctx,
			qtx,
			planned.touchedResources(operations, req.GetBuilds()),
		); lockErr != nil {
			return lockErr
		}
		for _, op := range operations {
			if opErr := a.apply(ctx, qtx, op); opErr != nil {
				return opErr
			}
		}
		if buildErr := a.deployBuilds(ctx, qtx); buildErr != nil {
			return buildErr
		}
		applied, getErr := qtx.GetEnvironmentByID(ctx, planned.env.ID)
		if getErr != nil {
			return getErr
		}
		revision = applied.Revision
		return nil
	})
	if errors.Is(err, errRevisionMoved) {
		current, getErr := s.queries.GetEnvironmentByID(ctx, planned.env.ID)
		if getErr != nil {
			slog.ErrorContext(ctx, "failed to get environment", "error", getErr, "environmentId", planned.env.ID)
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}
		return nil, applyRefused(errRevisionMoved, &planv1.ApplyRefusal{Revision: current.Revision})
	}
	if err != nil {
		return nil, applyTxError(ctx, err)
	}

	return &planv1.ApplyResponse{
		Revision:    revision,
		Operations:  planOperationsToProto(operations),
		Deployments: a.started,
	}, nil
}

// touchedResources are the existing resources the apply writes: those of the operations it
// performs and of the services the request deploys from a build.
func (p *plannedFile) touchedResources(operations []configplan.Operation, builds map[string]string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(operations)+len(builds))
	for _, op := range operations {
		if res, exists := p.live.resources[op.Service]; exists {
			ids = append(ids, res.ID)
		}
	}
	for service := range builds {
		if res, exists := p.live.resources[service]; exists {
			ids = append(ids, res.ID)
		}
	}
	return ids
}

func selectOperations(
	operations []configplan.Operation,
	provision []string,
	builds map[string]string,
) ([]configplan.Operation, error) {
	if len(provision) == 0 {
		return operations, nil
	}
	if len(builds) > 0 {
		return nil, rpcerr.New(connect.CodeInvalidArgument, errProvisionWithBuilds)
	}
	var selected []configplan.Operation
	for _, op := range operations {
		if !slices.Contains(provision, op.Service) {
			continue
		}
		if op.Kind == configplan.KindCreate {
			selected = append(selected, op)
		}
	}
	var refused []*planv1.PlanError
	for _, service := range provision {
		if !slices.ContainsFunc(selected, func(op configplan.Operation) bool { return op.Service == service }) {
			refused = append(refused, &planv1.PlanError{Service: service, Message: errProvisionNotPlanned.Error()})
		}
	}
	if len(refused) > 0 {
		return nil, applyRefused(errProvisionNotPlanned, &planv1.ApplyRefusal{Errors: refused})
	}
	return selected, nil
}

func (s *PlanServer) checkDeletes(ctx context.Context, planned *plannedFile, operations []configplan.Operation) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}
	for _, op := range operations {
		if op.Kind != configplan.KindDelete {
			continue
		}
		resourceID := planned.live.resources[op.Service].ID.String()
		deleteAction := actions.New(actions.DeleteResource, resourceID)
		if err := s.authz.Check(ctx, scopes, deleteAction); err != nil {
			slog.WarnContext(ctx, "unauthorized to delete resource through apply", "resourceId", resourceID)
			return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
		}
	}
	return nil
}

func (s *PlanServer) resolveBuilds(
	ctx context.Context,
	planned *plannedFile,
	requested map[string]string,
) (map[string]*deploymentv1.BuildSource, error) {
	builds := make(map[string]*deploymentv1.BuildSource, len(requested))
	for _, service := range slices.Sorted(maps.Keys(requested)) {
		desired, inFile := planned.plan.Desired[service]
		if !inFile {
			unknown := fmt.Errorf("%w: %s", errBuildServiceUnknown, service)
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(unknown)
		}
		if desired.Image != "" {
			hasImage := fmt.Errorf("%w: %s", errBuildServiceHasImage, service)
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(hasImage)
		}
		res, created := planned.live.resources[service]
		if !created {
			notCreated := fmt.Errorf("%w: %s", errBuildServiceNotCreated, service)
			return nil, rpcerr.New(connect.CodeFailedPrecondition, notCreated)
		}
		buildID := requested[service]
		src := &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID}
		pinned, err := pinDockerfileBuild(ctx, s.queries, res.ID, src)
		if err != nil {
			return nil, err
		}
		builds[service] = pinned
	}
	return builds, nil
}

func unconfirmedOperations(
	operations []configplan.Operation,
	confirmDestructive bool,
	confirmImport bool,
) []configplan.Operation {
	var unconfirmed []configplan.Operation
	for _, op := range operations {
		if op.Destructive && !confirmDestructive {
			unconfirmed = append(unconfirmed, op)
			continue
		}
		if op.Kind == configplan.KindImport && !confirmImport {
			unconfirmed = append(unconfirmed, op)
		}
	}
	return unconfirmed
}

func applyRefused(reason error, refusal *planv1.ApplyRefusal) error {
	refused := rpcerr.New(connect.CodeFailedPrecondition, reason)
	detail, err := connectproto.NewErrorDetail(refusal)
	if err != nil {
		return refused
	}
	refused = refused.WithDetail(detail)
	return refused
}

func applyTxError(ctx context.Context, err error) error {
	if connectErr, isConnect := errors.AsType[*connect.Error](err); isConnect {
		return connectErr
	}
	if errors.Is(err, errNoActiveCluster) {
		slog.WarnContext(ctx, "apply targets a region without a healthy cluster", "error", err)
		return rpcerr.New(connect.CodeFailedPrecondition, err)
	}
	if isPgConstraintViolation(err) {
		slog.WarnContext(ctx, "apply rejected by a constraint", "error", err)
		return rpcerr.New(connect.CodeAlreadyExists, errDomainInUse)
	}
	return deploymentTxError(ctx, err)
}
