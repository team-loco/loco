package rpc

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

func TestErrorDetail(t *testing.T) {
	other, err := connectproto.NewErrorDetail(&clusterv1.ClusterWorkloads{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := connectproto.NewErrorDetail(&buildv1.BuildsUnavailable{})
	if err != nil {
		t.Fatal(err)
	}
	withDetails := connect.NewError(connect.CodeFailedPrecondition, "refused").WithDetail(other).WithDetail(want)

	tests := map[string]struct {
		err  error
		want bool
	}{
		"matching detail":         {withDetails, true},
		"wrapped error":           {fmt.Errorf("start: %w", withDetails), true},
		"no details":              {connect.NewError(connect.CodeInternal, "boom"), false},
		"not a connect error":     {errors.New("plain"), false},
		"nil error":               {nil, false},
		"only other detail types": {connect.NewError(connect.CodeInternal, "x").WithDetail(other), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := ErrorDetail[*buildv1.BuildsUnavailable](tc.err)
			if ok != tc.want || (ok && got == nil) {
				t.Errorf("ErrorDetail = %v, %v; want found=%v", got, ok, tc.want)
			}
		})
	}
}
