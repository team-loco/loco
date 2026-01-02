package version

import (
	"fmt"
	"strings"
)

const (
	SpecVersionV1 int32 = 1
	SpecVersionV2 int32 = 2
)

// GetSpecVersionFromEndpoint extracts the spec version from a ConnectRPC endpoint path.
// e.g., "/user.v1.UserService/GetUser" -> SpecVersionV1
// e.g., "/user.v2.UserService/GetUser" -> SpecVersionV2
func GetSpecVersionFromEndpoint(endpoint string) (int32, error) {
	switch {
	case strings.Contains(endpoint, ".v1."):
		return SpecVersionV1, nil
	case strings.Contains(endpoint, ".v2."):
		return SpecVersionV2, nil
	default:
		return 0, fmt.Errorf("could not extract version from endpoint: %s", endpoint)
	}
}
