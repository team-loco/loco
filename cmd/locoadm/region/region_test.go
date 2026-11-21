package region

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/cmd/internal/clitest"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
)

type fakeRegions struct {
	clusterv1connect.UnimplementedRegionAdminServiceHandler
}

func (fakeRegions) ListRegions(
	context.Context,
	*clusterv1.ListRegionsRequest,
) (*clusterv1.ListRegionsResponse, error) {
	return &clusterv1.ListRegionsResponse{Regions: []*clusterv1.Region{{Code: "us-ca-1", ClusterCount: 2}}}, nil
}

func TestRegionListPrintsEachRegion(t *testing.T) {
	clitest.Session(t, func(server *connect.Server) {
		clusterv1connect.RegisterRegionAdminServiceHandler(server, fakeRegions{})
	})
	cmd := BuildRegionCmd()
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := &clusterv1.ListRegionsResponse{}
	if err := protojson.Unmarshal(output.Bytes(), got); err != nil {
		t.Fatalf("output = %s: %v", output, err)
	}
	want, err := fakeRegions{}.ListRegions(t.Context(), nil)
	if err != nil || !proto.Equal(got, want) {
		t.Fatalf("output = %s, want %v", output, want)
	}
}
