package loco

import (
	"context"
	"errors"
	"maps"
	"slices"

	"connectrpc.com/connect"
	"github.com/rogpeppe/go-internal/testscript"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
)

var errFakeUnknownEnvironment = errors.New("fakeapi: unknown environment")

type fakeSecret struct {
	value   string
	version int32
}

type fakeSecretService struct {
	secretv1connect.UnimplementedSecretServiceHandler

	api *fakeAPI
}

func (s *fakeSecretService) SetSecrets(
	_ context.Context,
	req *connect.Request[secretv1.SetSecretsRequest],
) (*connect.Response[secretv1.SetSecretsResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Msg.GetEnvironmentId() != fakeEnvironmentID {
		return nil, connect.NewError(connect.CodeNotFound, errFakeUnknownEnvironment)
	}
	values := req.Msg.GetValues()
	names := slices.Sorted(maps.Keys(values))
	versions := make([]*secretv1.SecretVersion, 0, len(names))
	for _, name := range names {
		version := f.platform.secrets[name].version + 1
		f.platform.secrets[name] = fakeSecret{value: values[name], version: version}
		versions = append(versions, &secretv1.SecretVersion{Name: name, Version: version})
	}
	f.platform.revision++
	resp := &secretv1.SetSecretsResponse{Revision: f.platform.revision, Versions: versions}
	return connect.NewResponse(resp), nil
}

func checkSecret(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if len(args) != 3 {
		ts.Fatalf("usage: fakeapi secret <name> <value>")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	secret, ok := api.platform.secrets[args[1]]
	if neg {
		if ok {
			ts.Fatalf("fakeapi: secret %s is set", args[1])
		}
		return
	}
	if !ok {
		ts.Fatalf("fakeapi: secret %s is not set", args[1])
	}
	if secret.value != args[2] {
		ts.Fatalf("fakeapi: secret %s has a different value than expected", args[1])
	}
}
