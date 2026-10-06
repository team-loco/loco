package main

import (
	"math"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const healthyStreamDuration = time.Minute

var reconnectBackoff = wait.Backoff{
	Duration: time.Second,
	Factor:   2,
	Jitter:   0.5,
	Steps:    math.MaxInt32,
	Cap:      30 * time.Second,
}
