package main

import (
	"errors"
	"fmt"
	"net"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/team-loco/loco/agent/pkg/applier"
)

func TestIsRetryable(t *testing.T) {
	resource := schema.GroupResource{Group: "infra.loco.io", Resource: "applications"}
	conflictReason := errors.New("modified")
	conflict := apierrors.NewConflict(resource, "resource-1", conflictReason)
	wrappedConflict := fmt.Errorf("failed to apply Application: %w", conflict)
	kind := schema.GroupKind{Group: "infra.loco.io", Kind: "Application"}
	invalid := apierrors.NewInvalid(kind, "resource-1", nil)
	serverTimeout := apierrors.NewServerTimeout(resource, "patch", 1)
	tooMany := apierrors.NewTooManyRequests("slow down", 1)
	timeout := apierrors.NewTimeoutError("timed out", 1)
	unavailable := apierrors.NewServiceUnavailable("down")
	forbiddenReason := errors.New("no")
	forbidden := apierrors.NewForbidden(resource, "resource-1", forbiddenReason)
	plain := errors.New("boom")
	dialErr := errors.New("connection refused")
	netErr := &net.OpError{Op: "dial", Net: "tcp", Err: dialErr}
	wrappedNetErr := fmt.Errorf("failed to apply Application: %w", netErr)
	badPayload := fmt.Errorf("%w: deploy payload is nil", applier.ErrInvalidPayload)

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "conflict", err: wrappedConflict, want: true},
		{name: "server timeout", err: serverTimeout, want: true},
		{name: "too many requests", err: tooMany, want: true},
		{name: "timeout", err: timeout, want: true},
		{name: "service unavailable", err: unavailable, want: true},
		{name: "net error", err: wrappedNetErr, want: true},
		{name: "invalid object", err: invalid, want: false},
		{name: "forbidden", err: forbidden, want: false},
		{name: "invalid payload", err: badPayload, want: false},
		{name: "plain error", err: plain, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryable(tc.err)
			if got != tc.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
