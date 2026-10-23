package main

import (
	"context"
	"crypto/ed25519"
	"errors"
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

var errUnexpectedRegistration = errors.New("unexpected registration")

type fakeRegistrar struct {
	agentv1connect.UnimplementedAgentServiceHandler
	t         *testing.T
	publicKey ed25519.PublicKey
	enrolled  int
	asserted  int
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
		if enrollment.GetJoinToken() != testJoinToken || f.publicKey != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, errUnexpectedRegistration.Error())
		}
		f.publicKey = enrollment.GetPublicKey()
		f.enrolled++
		return response, nil
	}
	info, _ := connect.CallInfoForServerContext(ctx)
	token, ok := strings.CutPrefix(info.RequestHeader().Get("Authorization"), "Bearer ")
	if !ok || f.publicKey == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errUnexpectedRegistration.Error())
	}
	claims, err := clusterassertion.Verify(token, f.publicKey, time.Now(), testAssertionTTL)
	if err != nil || claims.ClusterID != testClusterID {
		return nil, connect.NewError(connect.CodeUnauthenticated, errUnexpectedRegistration.Error())
	}
	f.asserted++
	return response, nil
}

func TestRegisterEnrollsOnceAndThenAuthenticatesWithTheStoredKey(t *testing.T) {
	kube := fake.NewClientset(&corev1.Namespace{
		Name: metav1.NamespaceSystem,
		UID:  types.UID(testClusterKey),
	})
	registrar := &fakeRegistrar{t: t}
	server := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(server, registrar)
	client := agentv1connect.NewAgentServiceClient(connect.NewClient(connectinprocess.New(server)))
	newAgent := func(joinToken string) *Agent {
		return &Agent{
			cfg:        &Config{JoinToken: joinToken, AssertionLifetime: testAssertionTTL},
			client:     client,
			inspector:  cluster.NewInspector(kube, testAgentNamespace, "loco-controller"),
			identities: identity.NewStore(kube, testAgentNamespace, testKeySecret),
		}
	}

	if err := newAgent("").register(t.Context()); !errors.Is(err, errNotEnrolled) {
		t.Fatalf("register without a key or join token: %v", err)
	}
	if err := newAgent(testJoinToken).register(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		restarted := newAgent(testJoinToken)
		if err := restarted.register(t.Context()); err != nil {
			t.Fatal(err)
		}
		if restarted.identity.ClusterID != testClusterID {
			t.Fatalf("cluster ID %q", restarted.identity.ClusterID)
		}
	}
	if err := newAgent("").register(t.Context()); err != nil {
		t.Fatalf("restart without the join token: %v", err)
	}
	if registrar.enrolled != 1 || registrar.asserted != 3 {
		t.Fatalf("enrolled %d times, asserted %d times", registrar.enrolled, registrar.asserted)
	}
}
