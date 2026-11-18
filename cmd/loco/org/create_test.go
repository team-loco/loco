package org

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/cmd/internal/clitest"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

type generatedNameServer struct {
	orgv1connect.UnimplementedOrgServiceHandler
}

func (generatedNameServer) CreateOrg(context.Context, *orgv1.CreateOrgRequest) (*orgv1.CreateOrgResponse, error) {
	return &orgv1.CreateOrgResponse{OrgId: testFirstOrgID, Name: testFirstOrgName}, nil
}

func TestCreateWithoutANamePrintsTheGeneratedName(t *testing.T) {
	clitest.Session(t, func(server *connect.Server) {
		orgv1connect.RegisterOrgServiceHandler(server, generatedNameServer{})
	})
	var output bytes.Buffer
	cmd := buildCreateCmd()
	cmd.SetOut(&output)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "Organization \"" + testFirstOrgName + "\" created successfully (ID: " + testFirstOrgID + ")\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}
