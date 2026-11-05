package v1alpha1

import "strconv"

const (
	KeyPlacementID       = "loco.io/placement-id"
	KeyPlacementRevision = "loco.io/placement-revision"

	envSecretNamePrefix = "env-"
)

// EnvSecretName is the name of the Secret in the agent's namespace that holds a placement's secret values.
func EnvSecretName(placementID string) string {
	return envSecretNamePrefix + placementID
}

// EnvSecretRevision reads the placement revision label of a staging Secret.
func EnvSecretRevision(labels map[string]string) (int64, bool) {
	return revisionOf(labels, KeyPlacementRevision)
}

func (a *Application) PlacementID() string {
	return a.GetAnnotations()[KeyPlacementID]
}

func (a *Application) PlacementRevision() (int64, bool) {
	return revisionOf(a.GetAnnotations(), KeyPlacementRevision)
}

// Placement reads the placement annotations; ok is false unless both are present.
func (a *Application) Placement() (id string, revision int64, ok bool) {
	id = a.PlacementID()
	if id == "" {
		return "", 0, false
	}
	revision, ok = a.PlacementRevision()
	if !ok {
		return "", 0, false
	}
	return id, revision, true
}

func revisionOf(values map[string]string, key string) (int64, bool) {
	value, ok := values[key]
	if !ok {
		return 0, false
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return revision, true
}
