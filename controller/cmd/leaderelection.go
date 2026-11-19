package main

import "time"

const (
	// A replica that stops renewing leaves the lease for up to leaseDuration before another takes it.
	leaseDuration = 60 * time.Second
	// The leader exits when it cannot renew within renewDeadline.
	renewDeadline = 45 * time.Second
	// The leader retries a failed renewal every retryPeriod.
	retryPeriod = 5 * time.Second
)
