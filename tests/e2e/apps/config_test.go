package apps

import (
	"testing"
	"time"

	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
)

func TestConfigReportsInstallHistoryRetention(t *testing.T) {
	t.Parallel()
	response, err := p.User.Config.GetConfig(t.Context(), &configv1.GetConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.GetObservabilityMaxTimeRange().AsDuration(); got != 24*time.Hour {
		t.Errorf("observability range = %v, want 24h", got)
	}
	retention := response.GetHistoryRetention()
	// The platform starts its API with LOCO_IMAGE_RETENTION=1.
	if retention.GetBuildImagesPerResource() != 1 {
		t.Errorf("build images per resource = %d, want 1", retention.GetBuildImagesPerResource())
	}
	if got := retention.GetEvents().AsDuration(); got != 90*24*time.Hour {
		t.Errorf("event retention = %v, want 90 days", got)
	}
}
