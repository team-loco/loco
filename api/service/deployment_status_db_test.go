package service

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestRecomputeDeploymentStatusAggregatesPlacements(t *testing.T) {
	t.Parallel()
	const (
		pending    = genDb.PlacementStatusPending
		deploying  = genDb.PlacementStatusDeploying
		running    = genDb.PlacementStatusRunning
		degraded   = genDb.PlacementStatusDegraded
		crashed    = genDb.PlacementStatusCrashed
		failed     = genDb.PlacementStatusFailed
		canceled   = genDb.PlacementStatusCanceled
		superseded = genDb.PlacementStatusSuperseded
	)
	for _, tt := range []struct {
		name       string
		placements [2]genDb.PlacementStatus
		canceledBy bool
		want       genDb.DeploymentStatus
	}{
		{"waiting for the build", [2]genDb.PlacementStatus{pending, pending}, false, genDb.DeploymentStatusBuilding},
		{"rolling out", [2]genDb.PlacementStatus{deploying, running}, false, genDb.DeploymentStatusDeploying},
		{"one region failed", [2]genDb.PlacementStatus{failed, running}, false, genDb.DeploymentStatusFailed},
		{"failed beats crashed", [2]genDb.PlacementStatus{failed, crashed}, false, genDb.DeploymentStatusFailed},
		{"one region crashed", [2]genDb.PlacementStatus{crashed, deploying}, false, genDb.DeploymentStatusCrashed},
		{"one region degraded", [2]genDb.PlacementStatus{degraded, running}, false, genDb.DeploymentStatusDegraded},
		{"running everywhere", [2]genDb.PlacementStatus{running, running}, false, genDb.DeploymentStatusRunning},
		{"partly replaced", [2]genDb.PlacementStatus{running, superseded}, false, genDb.DeploymentStatusRunning},
		{"replaced everywhere", [2]genDb.PlacementStatus{superseded, superseded}, false,
			genDb.DeploymentStatusSuperseded},
		{"a retired region is ignored", [2]genDb.PlacementStatus{canceled, running}, false,
			genDb.DeploymentStatusRunning},
		{"replaced before it ran", [2]genDb.PlacementStatus{canceled, canceled}, false,
			genDb.DeploymentStatusCanceled},
		{"canceled by a user", [2]genDb.PlacementStatus{running, deploying}, true, genDb.DeploymentStatusCanceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			params := f.paramsFor(f.clusterID).CreateDeploymentParams
			id, err := f.queries.CreateDeployment(t.Context(), params)
			if err != nil {
				t.Fatal(err)
			}
			for i, cluster := range []genDb.CreateDeploymentPlacementParams{
				{ClusterID: f.clusterID, Region: testRegion},
				{ClusterID: f.otherCluster, Region: otherRegion},
			} {
				cluster.DeploymentID, cluster.Replicas, cluster.Status = id, 1, tt.placements[i]
				if err = f.queries.CreateDeploymentPlacement(t.Context(), cluster); err != nil {
					t.Fatal(err)
				}
			}
			if tt.canceledBy {
				if _, err = f.pool.Exec(t.Context(),
					`UPDATE deployments SET canceled_by_type='user', canceled_by_id=$2 WHERE id=$1`,
					id, f.userID); err != nil {
					t.Fatal(err)
				}
			}
			status, err := f.queries.RecomputeDeploymentStatus(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if status.PreviousStatus != genDb.DeploymentStatusDeploying || status.Status != tt.want {
				t.Fatalf("status = %s from %s, want %s", status.Status, status.PreviousStatus, tt.want)
			}
			row, err := f.queries.GetDeploymentByID(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			terminal := tt.want == genDb.DeploymentStatusFailed || tt.want == genDb.DeploymentStatusCanceled ||
				tt.want == genDb.DeploymentStatusSuperseded
			if (row.CompletedAt != nil) != terminal {
				t.Fatalf("completed at = %v for %s", row.CompletedAt, tt.want)
			}
			started := tt.want == genDb.DeploymentStatusRunning || tt.want == genDb.DeploymentStatusDegraded
			if started && row.StartedAt == nil {
				t.Fatalf("started at unset for %s", tt.want)
			}
		})
	}
}

func TestConcurrentRegionReportsAggregateBothPlacements(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id, err := f.queries.CreateDeployment(t.Context(), f.paramsFor(f.clusterID).CreateDeploymentParams)
	if err != nil {
		t.Fatal(err)
	}
	clusters := []uuid.UUID{f.clusterID, f.otherCluster}
	regions := map[uuid.UUID]string{f.clusterID: testRegion, f.otherCluster: statusSecondRegion}
	for _, cluster := range clusters {
		if err := f.queries.CreateDeploymentPlacement(t.Context(), genDb.CreateDeploymentPlacementParams{
			DeploymentID: id, ClusterID: cluster, Region: regions[cluster], Replicas: 1,
			Status: genDb.PlacementStatusDeploying,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var updated, wg sync.WaitGroup
	updated.Add(len(clusters))
	errs := make(chan error, len(clusters))
	for _, cluster := range clusters {
		wg.Go(func() {
			errs <- pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
				qtx := genDb.New(tx)
				_, err := qtx.SetDeploymentPlacementStatus(t.Context(), genDb.SetDeploymentPlacementStatusParams{
					Status: genDb.PlacementStatusRunning, DeploymentID: id, ClusterID: cluster,
					FromStatuses: placementStatusNames(genDb.PlacementStatusDeploying),
				})
				updated.Done()
				updated.Wait()
				if err != nil {
					return err
				}
				_, err = recomputeDeploymentStatus(t.Context(), qtx, id)
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
		t.Fatalf("status = %s after both regions ran, want running", got)
	}
}
