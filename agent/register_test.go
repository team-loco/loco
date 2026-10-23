package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/types/known/durationpb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/team-loco/loco/agent/pkg/cluster"
	"github.com/team-loco/loco/agent/pkg/identity"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/internal/clusterassertion"
)

const (
	testClusterKey     = "00000000-0000-7000-8000-0000000000aa"
	testClusterID      = "00000000-0000-7000-8000-0000000000bb"
	testJoinToken      = "loco_j_test"
	testKeySecret      = "loco-core-agent-key" //nolint:gosec
	testAgentNamespace = "loco-system"
	testAssertionTTL   = time.Minute
)

var errRejectedCall = errors.New("call lacks a valid fresh assertion")

type fakeRegistrar struct {
	agentv1connect.UnimplementedAgentServiceHandler
	t         *testing.T
	publicKey ed25519.PublicKey
	jtis      map[string]bool
	enrolled  int
	asserted  int
	synced    int
}

func (f *fakeRegistrar) verify(ctx context.Context) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	token, ok := strings.CutPrefix(info.RequestHeader().Get("Authorization"), "Bearer ")
	if !ok || f.publicKey == nil {
		return connect.NewError(connect.CodeUnauthenticated, errRejectedCall.Error())
	}
	claims, err := clusterassertion.Verify(token, f.publicKey, time.Now(), testAssertionTTL)
	if err != nil || claims.ClusterID != testClusterID || f.jtis[claims.ID] {
		return connect.NewError(connect.CodeUnauthenticated, errRejectedCall.Error())
	}
	f.jtis[claims.ID] = true
	return nil
}

func (f *fakeRegistrar) Register(
	ctx context.Context,
	req *agentv1.RegisterRequest,
) (*agentv1.RegisterResponse, error) {
	if req.GetClusterKey() != testClusterKey {
		f.t.Errorf("cluster key %q", req.GetClusterKey())
	}
	response := &agentv1.RegisterResponse{ClusterId: testClusterID, HeartbeatInterval: durationpb.New(time.Second)}
	if enrollment := req.GetEnrollment(); enrollment != nil {
		info, _ := connect.CallInfoForServerContext(ctx)
		if header := info.RequestHeader().Get("Authorization"); header != "" {
			f.t.Errorf("enrollment sent Authorization %q", header)
		}
		if enrollment.GetJoinToken() != testJoinToken || f.publicKey != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, errRejectedCall.Error())
		}
		f.publicKey = enrollment.GetPublicKey()
		f.enrolled++
		return response, nil
	}
	if err := f.verify(ctx); err != nil {
		return nil, err
	}
	f.asserted++
	return response, nil
}

func (f *fakeRegistrar) Sync(ctx context.Context, _ agentv1connect.AgentServiceSyncServerStream) error {
	if err := f.verify(ctx); err != nil {
		return err
	}
	f.synced++
	return nil
}

func openTestSync(t *testing.T, a *Agent) {
	t.Helper()
	stream, err := a.client.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&agentv1.SyncRequest{}); err != nil {
		t.Logf("send: %v", err)
	}
	if _, err := stream.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("sync stream: %v", err)
	}
}

func TestEveryAgentCallCarriesAFreshAssertionOnceEnrolled(t *testing.T) {
	kube := fake.NewClientset(&corev1.Namespace{
		Name: metav1.NamespaceSystem,
		UID:  types.UID(testClusterKey),
	})
	registrar := &fakeRegistrar{t: t, jtis: map[string]bool{}}
	server := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(server, registrar)
	transport := connectinprocess.New(server)
	newAgent := func(joinToken string) *Agent {
		credentials := identity.NewCredentials(testAssertionTTL)
		connectClient := connect.NewClient(transport, credentials.Interceptor())
		return &Agent{
			cfg:         &Config{JoinToken: joinToken},
			client:      agentv1connect.NewAgentServiceClient(connectClient),
			inspector:   cluster.NewInspector(kube, testAgentNamespace, "loco-controller"),
			identities:  identity.NewStore(kube, testAgentNamespace, testKeySecret),
			credentials: credentials,
		}
	}

	if err := newAgent("").register(t.Context()); !errors.Is(err, errNotEnrolled) {
		t.Fatalf("register without a key or join token: %v", err)
	}
	enrolled := newAgent(testJoinToken)
	if err := enrolled.register(t.Context()); err != nil {
		t.Fatal(err)
	}
	openTestSync(t, enrolled)
	openTestSync(t, enrolled)
	for range 2 {
		restarted := newAgent(testJoinToken)
		if err := restarted.register(t.Context()); err != nil {
			t.Fatal(err)
		}
		openTestSync(t, restarted)
	}
	if err := newAgent("").register(t.Context()); err != nil {
		t.Fatalf("restart without the join token: %v", err)
	}
	if registrar.enrolled != 1 || registrar.asserted != 3 || registrar.synced != 4 {
		t.Fatalf("enrolled %d, asserted %d, synced %d", registrar.enrolled, registrar.asserted, registrar.synced)
	}
}
