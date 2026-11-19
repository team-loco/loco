package service

import (
	"testing"

	"connectrpc.com/connect/v2"
)

func TestClientAddress(t *testing.T) {
	t.Parallel()
	const (
		peer        = "10.0.0.5:4321"
		peerAddress = "10.0.0.5"
		spoofed     = "198.51.100.1"
		gateway     = "203.0.113.7"
		upstream    = "192.0.2.44"
		bad         = "unknown"
		peerV6      = "[2001:db8::2]:4321"
	)
	cases := []struct {
		name      string
		peer      string
		hops      int
		realIP    string
		forwarded []string
		want      string
	}{
		{name: "no trusted proxy ignores the header", peer: peer, forwarded: []string{spoofed}, want: peerAddress},
		{name: "real ip is ignored", peer: peer, hops: 1, realIP: spoofed, want: peerAddress},
		{
			name: "spoofed leftmost entry is ignored", peer: peer, hops: 1,
			forwarded: []string{spoofed + ", " + gateway}, want: gateway,
		},
		{
			name: "spoofed header line is ignored", peer: peer, hops: 1,
			forwarded: []string{spoofed, gateway}, want: gateway,
		},
		{
			name: "two proxies", peer: peer, hops: 2,
			forwarded: []string{spoofed + ", " + gateway + ", " + upstream}, want: gateway,
		},
		{name: "ipv6 entry", peer: peer, hops: 1, forwarded: []string{"2001:db8::1"}, want: "2001:db8::1"},
		{name: "header shorter than the hops", peer: peer, hops: 2, forwarded: []string{gateway}, want: peerAddress},
		{
			name:      "unparsable entry",
			peer:      peerV6,
			hops:      1,
			forwarded: []string{spoofed + ", " + bad},
			want:      "2001:db8::2",
		},
		{name: "no header uses peer", peer: peer, hops: 1, want: peerAddress},
		{name: "nothing", hops: 1, want: "invalid IP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &connect.CallInfo{PeerAddr: tc.peer}
			if tc.realIP != "" {
				info.RequestHeader().Set("X-Real-IP", tc.realIP)
			}
			for _, value := range tc.forwarded {
				info.RequestHeader().Add("X-Forwarded-For", value)
			}
			got := clientAddress(info, tc.hops)
			if got.String() != tc.want {
				t.Fatalf("clientAddress = %s, want %s", got, tc.want)
			}
		})
	}
}
