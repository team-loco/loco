package service

import (
	"testing"

	"connectrpc.com/connect"
)

func TestSubdomainLabelFor(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		want    string
		wantErr bool
	}{
		{name: "single label", domain: "myapp.onloco.app", want: "myapp"},
		{name: "other base domain", domain: "myapp.example.com", wantErr: true},
		{name: "nested label", domain: "a.b.onloco.app", wantErr: true},
		{name: "empty label", domain: ".onloco.app", wantErr: true},
		{name: "base domain itself", domain: "onloco.app", wantErr: true},
		{name: "suffix without dot", domain: "myapponloco.app", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := subdomainLabelFor(tt.domain, "onloco.app")
			if tt.wantErr {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("subdomainLabelFor: %v", err)
			}
			if got != tt.want {
				t.Fatalf("label = %q, want %q", got, tt.want)
			}
		})
	}
}
