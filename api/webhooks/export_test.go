package webhooks

import "time"

var Blocked = blocked

const (
	StatusPending   = statusPending
	StatusSucceeded = statusSucceeded
	StatusFailed    = statusFailed
)

func Lease(cfg Config) time.Duration { return cfg.lease() }
