package status

import "testing"

func TestAgentSkewPolicy(t *testing.T) {
	t.Parallel()
	const api = "v1.2.3"
	for _, tc := range []struct{ agent, want string }{
		{api, "supported"},
		{"v1.1.9", "supported"},
		{"v1.0.9", "unsupported"},
		{"v1.2.4", skewAhead},
		{"v1.3.0", skewAhead},
		{"v2.0.0", skewAhead},
		{"v0.9.0", "unsupported"},
		{"sha-test", skewUnknown},
		{"", skewUnknown},
	} {
		if got := skew(api, tc.agent); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.agent, got, tc.want)
		}
	}
}
