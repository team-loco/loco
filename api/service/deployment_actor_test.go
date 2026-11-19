package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestDeploymentCreationRequiresAPrincipal(t *testing.T) {
	t.Parallel()
	t.Run("direct", func(t *testing.T) {
		_, err := (&DeploymentServer{}).CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})
	t.Run("apply", func(t *testing.T) {
		err := (&applier{}).deploy(t.Context(), nil, genDb.Resource{}, nil, configplan.State{}, nil)
		wantCode(t, err, connect.CodeUnauthenticated)
	})
	t.Run("scale", func(t *testing.T) {
		err := (&ResourceServer{}).redeployRegions(t.Context(), nil, genDb.Resource{}, uuid.Nil, nil, events.Event{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})
}
