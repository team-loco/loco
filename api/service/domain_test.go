package service

import (
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
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

func TestDomainInputRejectsTheRemovedCustomType(t *testing.T) {
	const sourceField = 1
	const customField = 4
	const customType = 2
	payload := protowire.AppendTag(nil, sourceField, protowire.VarintType)
	payload = protowire.AppendVarint(payload, customType)
	payload = protowire.AppendTag(payload, customField, protowire.BytesType)
	payload = protowire.AppendString(payload, "app.example.com")
	input := &domainv1.DomainInput{}
	if err := proto.Unmarshal(payload, input); err != nil {
		t.Fatalf("decode legacy input: %v", err)
	}
	if err := protovalidate.Validate(input); err == nil {
		t.Fatal("custom domain type passed validation")
	}
}
