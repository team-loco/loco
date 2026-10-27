package labels

const (
	// keep in sync with labelBuildID in cmd/internal/logstream/logstream.go
	LabelBuildID         = "loco.io/build-id"
	LabelManagedBy       = "app.kubernetes.io/managed-by"
	LabelComponent       = "app.kubernetes.io/component"
	LabelOrganizationID  = "loco.io/organization-id"
	LabelWorkspaceID     = "loco.io/workspace-id"
	LabelResourceID      = "loco.io/resource-id"
	LabelEnvironmentID   = "loco.io/environment-id"
	ComponentApplication = "application"
	ManagedByController  = "loco-controller"
)
