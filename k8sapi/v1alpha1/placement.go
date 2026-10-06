package v1alpha1

import "strconv"

const (
	AnnotationPlacementID       = "loco.io/placement-id"
	AnnotationPlacementRevision = "loco.io/placement-revision"
)

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
