package webhooks

import "time"

var Blocked = blocked

func Lease(cfg Config) time.Duration { return cfg.lease() }
