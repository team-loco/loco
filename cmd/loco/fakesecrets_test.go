package loco

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/rogpeppe/go-internal/testscript"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var errFakeSecretNotFound = errors.New("secret not found")

const (
	fakeSecretUpdatedByType = "user"
	fakeSecretUpdatedByID   = "00000000-0000-7000-8000-0000000000a9"
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
	ctx context.Context,
	req *secretv1.SetSecretsRequest,
) (*secretv1.SetSecretsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: secretv1connect.SecretServiceSetSecretsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.platform.checkSecretTarget(req.GetResourceId(), req.GetEnvironmentId()); err != nil {
		return nil, err
	}
	values := req.GetValues()
	names := slices.Sorted(maps.Keys(values))
	versions := make([]*secretv1.SecretVersion, 0, len(names))
	for _, name := range names {
		key := fakeSecretKey{resourceID: req.GetResourceId(), environmentID: req.GetEnvironmentId(), name: name}
		version := f.platform.secrets[key].version + 1
		f.platform.secrets[key] = fakeSecret{value: values[name], version: version}
		versions = append(versions, &secretv1.SecretVersion{Name: name, Version: version})
	}
	f.platform.revision++
	resp := &secretv1.SetSecretsResponse{Revision: f.platform.revision, Versions: versions}
	return resp, nil
}

func (p *fakePlatform) checkSecretTarget(resourceID, environmentID string) error {
	if environmentID != fakeEnvironmentID {
		return connect.NewError(connect.CodeNotFound, errFakeUnknownEnvironment.Error()).
			WithCause(errFakeUnknownEnvironment)
	}
	for _, res := range p.resources {
		if res.GetId() == resourceID {
			return nil
		}
	}
	return connect.NewError(connect.CodeNotFound, errFakeResourceNotFound.Error()).WithCause(errFakeResourceNotFound)
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

func (s *fakeSecretService) DeleteSecrets(
	ctx context.Context,
	req *secretv1.DeleteSecretsRequest,
) (*secretv1.DeleteSecretsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: secretv1connect.SecretServiceDeleteSecretsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.platform.checkSecretTarget(req.GetResourceId(), req.GetEnvironmentId()); err != nil {
		return nil, err
	}
	keys := make([]fakeSecretKey, 0, len(req.GetNames()))
	for _, name := range req.GetNames() {
		key := fakeSecretKey{resourceID: req.GetResourceId(), environmentID: req.GetEnvironmentId(), name: name}
		keys = append(keys, key)
		if _, ok := f.platform.secrets[key]; !ok {
			missing := fmt.Errorf("%w: %s", errFakeSecretNotFound, name)
			return nil, connect.NewError(connect.CodeNotFound, missing.Error()).WithCause(missing)
		}
	}
	for _, key := range keys {
		delete(f.platform.secrets, key)
	}
	f.platform.revision++
	return &secretv1.DeleteSecretsResponse{Revision: f.platform.revision}, nil
}

func (s *fakeSecretService) ListSecrets(
	ctx context.Context,
	req *secretv1.ListSecretsRequest,
) (*secretv1.ListSecretsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f := s.api
	if _, _, err := f.authenticate(
		connect.Spec{Procedure: secretv1connect.SecretServiceListSecretsProcedure},
		info.RequestHeader(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.platform.checkSecretTarget(req.GetResourceId(), req.GetEnvironmentId()); err != nil {
		return nil, err
	}
	updated := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	versions := map[string]int32{}
	for key, secret := range f.platform.secrets {
		if key.resourceID == req.GetResourceId() && key.environmentID == req.GetEnvironmentId() {
			versions[key.name] = secret.version
		}
	}
	names := slices.Sorted(maps.Keys(versions))
	secrets := make([]*secretv1.Secret, 0, len(names))
	for _, name := range names {
		secrets = append(secrets, &secretv1.Secret{
			Name:          name,
			Version:       versions[name],
			UpdatedByType: fakeSecretUpdatedByType,
			UpdatedById:   fakeSecretUpdatedByID,
			CreatedAt:     timestamppb.New(updated),
			UpdatedAt:     timestamppb.New(updated),
		})
	}
	return &secretv1.ListSecretsResponse{Secrets: secrets}, nil
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
