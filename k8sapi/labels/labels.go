package labels

const (
	// keep in sync with labelBuildID in cmd/internal/logstream/logstream.go
	LabelBuildID   = "loco.io/build-id"
	LabelManagedBy = "app.kubernetes.io/managed-by"
)
