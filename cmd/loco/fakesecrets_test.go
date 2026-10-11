package loco

import (
	"context"
	"maps"
	"slices"

	"connectrpc.com/connect"
	"github.com/rogpeppe/go-internal/testscript"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
)

type fakeSecret struct {
	value   string
	version int32
}

type fakeSecretKey struct {
	resourceID    string
	environmentID string
	name          string
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
	if err := f.platform.checkSecretTarget(req.Msg.GetResourceId(), req.Msg.GetEnvironmentId()); err != nil {
		return nil, err
	}
	values := req.Msg.GetValues()
	names := slices.Sorted(maps.Keys(values))
	versions := make([]*secretv1.SecretVersion, 0, len(names))
	for _, name := range names {
		key := fakeSecretKey{resourceID: req.Msg.GetResourceId(), environmentID: req.Msg.GetEnvironmentId(), name: name}
		version := f.platform.secrets[key].version + 1
		f.platform.secrets[key] = fakeSecret{value: values[name], version: version}
		versions = append(versions, &secretv1.SecretVersion{Name: name, Version: version})
	}
	f.platform.revision++
	resp := &secretv1.SetSecretsResponse{Revision: f.platform.revision, Versions: versions}
	return connect.NewResponse(resp), nil
}

func (p *fakePlatform) checkSecretTarget(resourceID, environmentID string) error {
	if environmentID != fakeEnvironmentID {
		return connect.NewError(connect.CodeNotFound, errFakeUnknownEnvironment)
	}
	for _, res := range p.resources {
		if res.GetId() == resourceID {
			return nil
		}
	}
	return connect.NewError(connect.CodeNotFound, errFakeResourceNotFound)
}

func (p *fakePlatform) secretKey(ts *testscript.TestScript, service, name string) fakeSecretKey {
	for _, res := range p.resources {
		if res.GetName() == service {
			return fakeSecretKey{resourceID: res.GetId(), environmentID: fakeEnvironmentID, name: name}
		}
	}
	ts.Fatalf("fakeapi: no service %s", service)
	return fakeSecretKey{}
}

func checkSecret(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if len(args) != 4 {
		ts.Fatalf("usage: fakeapi secret <service> <name> <value>")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	service, name := args[1], args[2]
	secret, ok := api.platform.secrets[api.platform.secretKey(ts, service, name)]
	if neg {
		if ok {
			ts.Fatalf("fakeapi: secret %s of %s is set", name, service)
		}
		return
	}
	if !ok {
		ts.Fatalf("fakeapi: secret %s of %s is not set", name, service)
	}
	if secret.value != args[3] {
		ts.Fatalf("fakeapi: secret %s of %s has a different value than expected", name, service)
	}
}
