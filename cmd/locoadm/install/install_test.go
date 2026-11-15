package install

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	"github.com/team-loco/loco/gen/go/loco/admin/v1/adminv1connect"
)

func TestInstallationCommandsRejectNonpositiveTimeoutBeforeReadingInputs(t *testing.T) {
	for _, command := range Commands() {
		t.Run(command.Name(), func(t *testing.T) {
			command.SetArgs([]string{"--file", "missing-loco-install.yaml", "--timeout", "0s"})
			if err := command.Execute(); !errors.Is(err, errTimeout) {
				t.Fatalf("got %v, want invalid timeout before reading configuration", err)
			}
		})
	}
}

type fakeAdmins struct {
	adminv1connect.UnimplementedSystemAdminServiceHandler
	unclaimed []string
}

func (f *fakeAdmins) ListSystemAdmins(
	context.Context, *adminv1.ListSystemAdminsRequest,
) (*adminv1.ListSystemAdminsResponse, error) {
	return &adminv1.ListSystemAdminsResponse{UnclaimedEmails: f.unclaimed}, nil
}

func TestReportAdminsAsksTheAPIOnlyWithASession(t *testing.T) {
	server := connect.NewServer()
	adminv1connect.RegisterSystemAdminServiceHandler(server, &fakeAdmins{unclaimed: []string{"grace@example.com"}})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	config := installation.Config{ControlPlane: installation.ControlPlane{
		APIURL: api.URL, Admins: []string{"ada@example.com", "grace@example.com"},
	}}

	var output bytes.Buffer
	if err := reportAdmins(t.Context(), &output, config); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "Configured admins: ada@example.com, grace@example.com.") {
		t.Fatalf("report without a session = %q, want every configured admin", output.String())
	}

	store, err := keychain.ForCurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	session := keychain.UserToken{Host: api.URL, Token: "session", ExpiresAt: time.Now().Add(time.Hour)}
	if err = store.Set(session); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = reportAdmins(t.Context(), &output, config); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Configured admins who have not signed in: grace@example.com\n" {
		t.Fatalf("report with a session = %q, want the unclaimed admin", output.String())
	}
}
