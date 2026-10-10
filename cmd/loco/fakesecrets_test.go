package loco

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/rogpeppe/go-internal/testscript"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errFakeUnknownEnvironment = errors.New("fakeapi: unknown environment")
	errFakeSecretNotFound     = errors.New("secret not found")
)

const (
	fakeSecretUpdatedByType = "user"
	fakeSecretUpdatedByID   = "00000000-0000-7000-8000-0000000000a9"
)

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

func (s *fakeSecretService) DeleteSecrets(
	_ context.Context,
	req *connect.Request[secretv1.DeleteSecretsRequest],
) (*connect.Response[secretv1.DeleteSecretsResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, name := range req.Msg.GetNames() {
		if _, ok := f.platform.secrets[name]; !ok {
			missing := fmt.Errorf("%w: %s", errFakeSecretNotFound, name)
			return nil, connect.NewError(connect.CodeNotFound, missing)
		}
	}
	for _, name := range req.Msg.GetNames() {
		delete(f.platform.secrets, name)
	}
	f.platform.revision++
	return connect.NewResponse(&secretv1.DeleteSecretsResponse{Revision: f.platform.revision}), nil
}

func (s *fakeSecretService) ListSecrets(
	_ context.Context,
	req *connect.Request[secretv1.ListSecretsRequest],
) (*connect.Response[secretv1.ListSecretsResponse], error) {
	f := s.api
	if _, _, err := f.authenticate(req.Spec(), req.Header()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	updated := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	names := slices.Sorted(maps.Keys(f.platform.secrets))
	secrets := make([]*secretv1.Secret, 0, len(names))
	for _, name := range names {
		secrets = append(secrets, &secretv1.Secret{
			Name:          name,
			Version:       f.platform.secrets[name].version,
			UpdatedByType: fakeSecretUpdatedByType,
			UpdatedById:   fakeSecretUpdatedByID,
			CreatedAt:     timestamppb.New(updated),
			UpdatedAt:     timestamppb.New(updated),
		})
	}
	return connect.NewResponse(&secretv1.ListSecretsResponse{Secrets: secrets}), nil
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
