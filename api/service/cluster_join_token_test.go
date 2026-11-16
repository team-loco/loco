package service

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestJoinTokenTTL(t *testing.T) {
	t.Parallel()
	server := &ClusterAdminServer{joinTokenTTL: time.Hour, joinTokenMaxTTL: 7 * 24 * time.Hour}
	for _, tc := range []struct {
		name string
		ttl  *durationpb.Duration
		want time.Duration
	}{
		{"default", nil, time.Hour},
		{"custom", durationpb.New(72 * time.Hour), 72 * time.Hour},
		{"maximum", durationpb.New(7 * 24 * time.Hour), 7 * 24 * time.Hour},
		{"zero", durationpb.New(0), 0},
		{"negative", durationpb.New(-time.Second), 0},
		{"over maximum", durationpb.New(7*24*time.Hour + time.Second), 0},
		{"overflow", &durationpb.Duration{Seconds: 315576000000}, 0},
		{"invalid nanos", &durationpb.Duration{Seconds: 1, Nanos: -1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := server.tokenTTL(tc.ttl)
			if tc.want == 0 {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("ttl: %v, want invalid argument", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ttl = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
