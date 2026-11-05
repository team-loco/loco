package main

import (
	"math"

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
