package infra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
	"google.golang.org/protobuf/proto"
)

func TestDirectoryServiceName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My App")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	name, err := directoryServiceName()
	if err != nil || name != "my-app" {
		t.Fatalf("directoryServiceName() = %q, %v; want my-app", name, err)
	}

	starter := locofile.Starter{
		SchemaURL: "https://loco.test/schemas/loco.v1.json", Name: name,
		Region: "us-ca-1", CPU: "100m", Memory: "256Mi", MinReplicas: 1, MaxReplicas: 2,
	}
	data, err := starter.Render()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locofile.Parse(data); err != nil {
		t.Fatalf("starter for %q does not validate: %v", name, err)
	}
}

func TestDirectoryServiceNameRejectsUnusableDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "!!!")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if _, err := directoryServiceName(); !errors.Is(err, errUnsafeDirectoryName) {
		t.Fatalf("directoryServiceName() error = %v, want %v", err, errUnsafeDirectoryName)
	}
}

func TestDomainSuffix(t *testing.T) {
	first := domainSuffix("workspace-a", "production", "web")
	if !isDomainSuffix(first) {
		t.Fatalf("domainSuffix() = %q, want four lowercase letters", first)
	}
	if got := domainSuffix("workspace-a", "production", "web"); got != first {
		t.Fatalf("domainSuffix() = %q, want stable %q", got, first)
	}
	if got := domainSuffix("workspace-b", "production", "web"); got == first {
		t.Fatalf("another workspace returned the same suffix %q", got)
	}
}

func TestInitRejectsInvalidServiceName(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd := buildInitCmd()
	for _, name := range []string{"!!!", "My App"} {
		cmd.SetArgs([]string{"--name", name})
		if err := cmd.Execute(); !errors.Is(err, locofile.ErrInvalidName) {
			t.Fatalf("init --name %q error = %v, want %v", name, err, locofile.ErrInvalidName)
		}
	}
}

func TestDomainBaseLeavesRoomForTheSuffix(t *testing.T) {
	if got := domainBase("checkout-api"); got != "checkout-api-" {
		t.Fatalf("domainBase() = %q, want checkout-api-", got)
	}
	if got := domainBase(strings.Repeat("a", 52) + "-b"); len(got)+domainSuffixLength > locofile.MaxServiceNameLength {
		t.Fatalf("domainBase() = %q leaves no room for the suffix", got)
	}
}

func TestGeneratedDomainEnvironment(t *testing.T) {
	productionDomain := &domainv1.ResourceDomain{Domain: "web-prod.onloco.test", EnvironmentId: "production-id"}
	stagingDomain := &domainv1.ResourceDomain{Domain: "web-stag.onloco.test", EnvironmentId: "staging-id"}
	for _, tc := range []struct {
		name    string
		domains []*domainv1.ResourceDomain
		staging string
	}{
		{
			name:    "reuse only the selected environment",
			domains: []*domainv1.ResourceDomain{productionDomain, stagingDomain},
			staging: stagingDomain.GetDomain(),
		},
		{name: "generate beside another environment", domains: []*domainv1.ResourceDomain{productionDomain}},
		{name: "generate distinct stable hostnames"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := proto.Marshal(&resourcev1.GetResourceResponse{
				Resource: &resourcev1.Resource{Domains: tc.domains},
			})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/proto")
				if strings.HasSuffix(r.URL.Path, "/GetResource") {
					if _, writeErr := w.Write(response); writeErr != nil {
						t.Error(writeErr)
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, "/CheckDomainAvailability") {
					data, marshalErr := proto.Marshal(&domainv1.CheckDomainAvailabilityResponse{IsAvailable: true})
					if marshalErr != nil {
						t.Error(marshalErr)
						return
					}
					if _, writeErr := w.Write(data); writeErr != nil {
						t.Error(writeErr)
					}
					return
				}
				t.Errorf("unexpected request: %s", r.URL.Path)
				http.NotFound(w, r)
			}))
			defer server.Close()
			target := cmdutil.Target{
				Clients:     rpc.NewHTTPClients(server.Client(), server.URL, ""),
				WorkspaceID: "workspace-id",
				Environment: &environmentv1.Environment{Id: "production-id"},
			}
			production, err := generatedDomain(context.Background(), target, "web", "onloco.test")
			if err != nil {
				t.Fatal(err)
			}
			target.Environment = &environmentv1.Environment{Id: "staging-id"}
			staging, err := generatedDomain(context.Background(), target, "web", "onloco.test")
			if err != nil {
				t.Fatal(err)
			}
			if staging == production {
				t.Fatalf("production and staging share %q", staging)
			}
			if tc.staging != "" && staging != tc.staging {
				t.Fatalf("staging hostname = %q, want its existing %q", staging, tc.staging)
			}
			again, err := generatedDomain(context.Background(), target, "web", "onloco.test")
			if err != nil || again != staging {
				t.Fatalf("staging retry = %q, %v; want stable %q", again, err, staging)
			}
		})
	}
}
