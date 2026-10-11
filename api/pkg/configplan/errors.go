package configplan

import "errors"

var (
	ErrOwnedByOtherPartial = errors.New("service is owned by another partial")
	ErrUnknownRegion       = errors.New("region has no active cluster")
	ErrUnknownEnvironment  = errors.New("environment does not exist in the workspace")
	ErrMissingSecret       = errors.New("secret is not set in the environment")
	ErrCustomDomain        = errors.New("domain is not under an active platform domain")
	ErrNotSingleLabel      = errors.New("domain must be a single label under its platform domain")
	ErrImageUnresolved     = errors.New("image could not be resolved")
	ErrImageNotResolved    = errors.New("no resolution was supplied for the image")
)
