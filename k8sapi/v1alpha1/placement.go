package v1alpha1

import "strconv"

const (
	AnnotationPlacementID       = "loco.io/placement-id"
	AnnotationPlacementRevision = "loco.io/placement-revision"
	LabelPlacementID            = "loco.io/placement-id"
	LabelPlacementRevision      = "loco.io/placement-revision"

	envSecretNamePrefix = "env-"
)

// EnvSecretName is the name of the Secret in the agent's namespace that holds a placement's secret values.
func EnvSecretName(placementID string) string {
	return envSecretNamePrefix + placementID
}

// EnvSecretRevision reads the placement revision label of a staging Secret.
func EnvSecretRevision(labels map[string]string) (int64, bool) {
	value, ok := labels[LabelPlacementRevision]
	if !ok {
		return 0, false
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return revision, true
}

func (a *Application) PlacementID() string {
	return a.GetAnnotations()[AnnotationPlacementID]
}

func (a *Application) PlacementRevision() (int64, bool) {
	value, ok := a.GetAnnotations()[AnnotationPlacementRevision]
	if !ok {
		return 0, false
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return revision, true
}
