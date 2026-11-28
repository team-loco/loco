package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestDeploymentCommitMessageComesFromBuild(t *testing.T) {
	f := newBuildFixture(t)
	seedDeployment(t, f.deployFixture, sameRegionSpec)
	buildID := f.create(t, 10).GetBuildId()
	const subject = "Fix the checkout total"
	if _, err := f.pool.Exec(
		f.ctx, `UPDATE builds SET git_commit_message = $2 WHERE id = $1`, buildID, subject,
	); err != nil {
		t.Fatal(err)
	}

	spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()}}
	created, err := deployRequest(t, f.deployFixture, &deploymentv1.CreateDeploymentRequest{
		RequestId:     uuid.NewString(),
		ResourceId:    f.resourceID.String(),
		Regions:       []string{testRegion},
		EnvironmentId: f.envID.String(),
		Spec:          spec,
		Commit:        &deploymentv1.SourceCommit{Sha: strings.Repeat("a", 40)},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetDeploymentId()
	if got := f.getDeployment(t, id).GetCommit().GetMessage(); got != "" {
		t.Fatalf("message without a build = %q", got)
	}

	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE deployments SET spec = jsonb_set(spec, '{build,buildId}', to_jsonb($2::text)) WHERE id = $1`,
		id, buildID,
	); err != nil {
		t.Fatal(err)
	}
	if got := f.getDeployment(t, id).GetCommit().GetMessage(); got != subject {
		t.Fatalf("message = %q, want %q", got, subject)
	}
}
