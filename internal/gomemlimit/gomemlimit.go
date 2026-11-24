// Package gomemlimit sets the Go memory limit from the container's cgroup memory limit.
package gomemlimit

import (
	"errors"
	"log/slog"

	"github.com/KimMachineGun/automemlimit/memlimit"
)

// Set sets the Go memory limit to AUTOMEMLIMIT (0.9 by default) of the cgroup limit unless GOMEMLIMIT is set.
func Set(logger *slog.Logger) {
	memlimit.Set(memlimit.WithLogger(logger), memlimit.WithProvider(cgroupLimit))
}

// cgroupLimit treats a host without cgroups, such as macOS, as one without a limit.
func cgroupLimit() (uint64, error) {
	limit, err := memlimit.FromCgroup()
	if errors.Is(err, memlimit.ErrCgroupsNotSupported) {
		return 0, memlimit.ErrNoLimit
	}
	return limit, err
}
