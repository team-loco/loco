package main

import (
	"errors"
	"math"
	"net"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsConflict(err) {
		return true
	}
	if apierrors.IsServerTimeout(err) {
		return true
	}
	if apierrors.IsTooManyRequests(err) {
		return true
	}
	if apierrors.IsTimeout(err) {
		return true
	}
	if apierrors.IsServiceUnavailable(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
