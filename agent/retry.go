package main

import (
	"math"

	"connectrpc.com/connect/v2"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	reconnectFactor = 2
	reconnectJitter = 0.5
)

func newReconnectBackoff(cfg *Config) wait.Backoff {
	return wait.Backoff{
		Duration: cfg.ReconnectBaseDelay,
		Factor:   reconnectFactor,
		Jitter:   reconnectJitter,
		Steps:    math.MaxInt32,
		Cap:      cfg.ReconnectMaxDelay,
	}
}

func retryable(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied:
		return false
	case connect.CodeUnauthenticated:
		return false
	case connect.CodeResourceExhausted:
		return false
	default:
		return true
	}
}
