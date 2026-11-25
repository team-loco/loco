package audit

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/cmd/internal/clitest"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
)

type fakeEvents struct {
	eventv1connect.UnimplementedEventServiceHandler
	requests []*eventv1.ListEventsRequest
}

func (f *fakeEvents) ListEvents(
	_ context.Context, req *eventv1.ListEventsRequest,
) (*eventv1.ListEventsResponse, error) {
	f.requests = append(f.requests, req)
	at := timestamppb.New(timestamppb.Now().AsTime().UTC().Truncate(1e9))
	return &eventv1.ListEventsResponse{Events: []*eventv1.Event{
		{Type: "org.suspended", OrgId: "org-1", ActorEmail: "ops@loco.test", ActedAsOperator: true, CreatedAt: at},
		{Type: "cluster.paused", ActorType: "user", ActorName: "Ada", CreatedAt: at},
	}}, nil
}

func runAuditCmd(t *testing.T, fake *fakeEvents, args ...string) (string, error) {
	t.Helper()
	clitest.Session(t, func(server *connect.Server) {
		eventv1connect.RegisterEventServiceHandler(server, fake)
	})
	cmd := BuildAuditCmd()
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

func TestAuditListsEventsAndLabelsOperators(t *testing.T) {
	orgID := uuid.NewString()
	fake := &fakeEvents{}
	output, err := runAuditCmd(t, fake, "--org", orgID, "--query", "@type:org.suspended", "--limit", "5")
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, output)
	}
	req := fake.requests[0]
	if req.GetOrgId() != orgID || req.GetQuery() != "@type:org.suspended" || req.GetPageSize() != 5 ||
		req.GetInstallationOnly() {
		t.Fatalf("request = %v", req)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "TIME") {
		t.Fatalf("output = %q", output)
	}
	if !strings.Contains(lines[1], "ops@loco.test (Loco admin)") || !strings.Contains(lines[1], "org-1") {
		t.Fatalf("operator row = %q", lines[1])
	}
	if !strings.Contains(lines[2], "Ada") || strings.Contains(lines[2], "Loco admin") ||
		!strings.Contains(lines[2], "-") {
		t.Fatalf("installation row = %q", lines[2])
	}
}

func TestAuditInstallationOnlySendsTheFilter(t *testing.T) {
	fake := &fakeEvents{}
	if output, err := runAuditCmd(t, fake, "--installation-only"); err != nil {
		t.Fatalf("audit: %v\n%s", err, output)
	}
	if req := fake.requests[0]; !req.GetInstallationOnly() || req.GetOrgId() != "" {
		t.Fatalf("request = %v", req)
	}
}

func TestAuditRejectsAnOrganizationWithInstallationOnly(t *testing.T) {
	if _, err := runAuditCmd(t, &fakeEvents{}, "--installation-only", "--org", uuid.NewString()); err == nil {
		t.Fatal("audit accepted --org with --installation-only")
	}
}
