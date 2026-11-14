package labels

const (
	ControllerManagedBy = "loco-controller"
	// keep in sync with labelBuildID in cmd/internal/logstream/logstream.go
	LabelBuildID        = "loco.io/build-id"
	LabelManagedBy      = "app.kubernetes.io/managed-by"
	LabelOrganizationID = "loco.io/organization-id"
	LabelWorkspaceID    = "loco.io/workspace-id"
	LabelResourceID     = "loco.io/resource-id"
	LabelEnvironmentID  = "loco.io/environment-id"
	// keep in sync with the namespace selectors in charts/loco-worker/templates
	LabelLocoApp             = "loco.io/app"
	LabelPrivateServiceOwner = "loco.io/private-service-owner"

	AnnotationApplicationNamespace = "loco.io/application-namespace"
	AnnotationApplicationName      = "loco.io/application-name"
	AnnotationEnvSecretRevision    = "loco.io/env-secret-revision" //nolint:gosec
)
