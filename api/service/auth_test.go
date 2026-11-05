package service

import (
	"testing"

	"connectrpc.com/connect/v2"
)

func TestClientAddress(t *testing.T) {
	const (
		peer        = "10.0.0.5:4321"
		peerAddress = "10.0.0.5"
		realIP      = "192.0.2.9"
		first       = "198.51.100.1"
		second      = "203.0.113.7"
		bad         = "unknown"
		peerV6      = "[2001:db8::2]:4321"
	)
	cases := []struct {
		name      string
		peer      string
		realIP    string
		forwarded []string
		want      string
	}{
		{name: "real ip wins", peer: peer, realIP: realIP, forwarded: []string{first}, want: realIP},
		{name: "first comma-separated entry", peer: peer, forwarded: []string{first + ", " + second}, want: first},
		{name: "first header line", peer: peer, forwarded: []string{first, second}, want: first},
		{name: "ipv6 entry", peer: peer, forwarded: []string{"2001:db8::1"}, want: "2001:db8::1"},
		{name: "unparsable real ip", peer: peer, realIP: bad, forwarded: []string{first}, want: first},
		{name: "unparsable entry", peer: peer, forwarded: []string{bad + ", " + second}, want: second},
		{name: "unparsable both", peer: peerV6, realIP: bad, forwarded: []string{bad}, want: "2001:db8::2"},
		{name: "no header uses peer", peer: peer, want: peerAddress},
		{name: "nothing", want: "invalid IP"},
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
			got := clientAddress(info)
			if got.String() != tc.want {
				t.Fatalf("clientAddress = %s, want %s", got, tc.want)
			}
		})
	}
}
