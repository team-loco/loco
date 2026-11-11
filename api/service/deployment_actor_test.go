package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestDeploymentCreationRequiresAPrincipal(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		_, err := (&DeploymentServer{}).CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})
	t.Run("apply", func(t *testing.T) {
		err := (&applier{}).deploy(t.Context(), nil, genDb.Resource{}, nil, configplan.State{}, nil)
		wantCode(t, err, connect.CodeUnauthenticated)
	})
	t.Run("scale", func(t *testing.T) {
		_, err := (&ResourceServer{}).planRegionRedeploy(t.Context(), nil, genDb.Deployment{}, nil, 1, "")
		wantCode(t, err, connect.CodeUnauthenticated)
	})
}
