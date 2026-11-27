package main

import (
	"strings"
	"testing"
)

func TestDestinationsRequireExactClusterMembership(t *testing.T) {
	b := binding{
		Destinations: []destination{
			{ClusterIDs: []string{testCA}, Hostname: testTarget},
			{ClusterIDs: []string{testNY}, Hostname: testNYTarget},
			{ClusterIDs: []string{testCA, testNY}, Hostname: testBothTarget},
		},
	}
	for _, tc := range []struct {
		ids  []string
		want string
	}{
		{[]string{testCA}, testTarget},
		{[]string{testNY}, testNYTarget},
		{[]string{testCA, testNY}, testBothTarget},
		{[]string{testNY, testCA}, testBothTarget},
		{nil, ""}, {[]string{testCA, testCA}, ""}, {[]string{testPublisherID}, ""}, {[]string{"bad-id"}, ""},
	} {
		got, err := b.target(tc.ids)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Fatalf("target(%v)=%q,%v", tc.ids, got, err)
		}
	}
}
func TestConfigurationRejectsDuplicateSetsAndUnsafeEndpoints(t *testing.T) {
	c := config{
		APIURL:         "https://api.example.test",
		PollSeconds:    5,
		AuditSeconds:   86400,
		BatchSize:      3,
		RequestSeconds: 10,
		Bindings: []binding{
			{
				AppDomain:           "apps.example.test",
				PublisherID:         testPublisherID,
				ZoneID:              strings.Repeat("a", 32),
				LocoTokenFile:       "/secret/loco",
				CloudflareTokenFile: "/secret/provider",
				Destinations:        []destination{{ClusterIDs: []string{testCA, testNY}, Hostname: testTarget}},
			},
		},
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.Bindings[0].Destinations = append(
		c.Bindings[0].Destinations,
		destination{ClusterIDs: []string{testNY, testCA}, Hostname: testBothTarget},
	)
	if err := c.validate(); err == nil {
		t.Fatal("duplicate exact sets accepted")
	}
	c.Bindings[0].Destinations = c.Bindings[0].Destinations[:1]
	for _, endpoint := range []string{
		"http://api.example.test", "https://secret@api.example.test", "https://api.example.test?token=secret",
	} {
		c.APIURL = endpoint
		if err := c.validate(); err == nil {
			t.Fatalf("unsafe API URL accepted: %s", endpoint)
		}
	}
}
