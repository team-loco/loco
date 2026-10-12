package interceptor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

func TestAuthRejectsBeforeDecode(t *testing.T) {
	server := connect.NewServer(NewAuthInterceptor(nil, nil, nil, nil).Interceptor())
	orgv1connect.RegisterOrgServiceHandler(server, &orgv1connect.UnimplementedOrgServiceHandler{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	body := bytes.NewBufferString("invalid JSON")
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		srv.URL+orgv1connect.OrgServiceCreateOrgProcedure,
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var failure struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "unauthenticated" {
		t.Fatalf("code = %q, want unauthenticated", failure.Code)
	}
}
