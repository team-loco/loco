package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/rogpeppe/go-internal/testscript"
	"google.golang.org/protobuf/types/known/timestamppb"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errFakeSecretNotFound = errors.New("secret not found")
	errFakeSecretInUse    = errors.New("secret is read by the latest deployment of a resource")
)

const (
	fakeSecretUpdatedByType = "user"
	fakeSecretUpdatedByID   = "00000000-0000-7000-8000-0000000000a9"
)

type fakeSecret struct {
	value   string
	version int32
	// users are the slugs of the services whose latest deployment reads the secret.
	users []string
}

type fakeSecretKey struct {
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
	f := s.api
	if err := f.authorizeSecrets(
		ctx, secretv1connect.SecretServiceSetSecretsProcedure, req.GetEnvironmentId(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	values := req.GetValues()
	names := slices.Sorted(maps.Keys(values))
	versions := make([]*secretv1.SecretVersion, 0, len(names))
	var users []string
	for _, name := range names {
		key := fakeSecretKey{environmentID: req.GetEnvironmentId(), name: name}
		previous, existed := f.platform.secrets[key]
		f.platform.secrets[key] = fakeSecret{value: values[name], version: previous.version + 1, users: previous.users}
		versions = append(versions, &secretv1.SecretVersion{Name: name, Version: previous.version + 1})
		if existed && previous.value != values[name] {
			users = append(users, previous.users...)
		}
	}
	f.platform.revision++
	slices.Sort(users)
	return &secretv1.SetSecretsResponse{
		Revision: f.platform.revision,
		Versions: versions,
		UsedBy:   f.platform.secretUsers(slices.Compact(users)),
	}, nil
}

func (s *fakeSecretService) DeleteSecrets(
	ctx context.Context,
	req *secretv1.DeleteSecretsRequest,
) (*secretv1.DeleteSecretsResponse, error) {
	f := s.api
	if err := f.authorizeSecrets(
		ctx, secretv1connect.SecretServiceDeleteSecretsProcedure, req.GetEnvironmentId(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]fakeSecretKey, 0, len(req.GetNames()))
	for _, name := range req.GetNames() {
		key := fakeSecretKey{environmentID: req.GetEnvironmentId(), name: name}
		keys = append(keys, key)
		secret, ok := f.platform.secrets[key]
		if !ok {
			missing := fmt.Errorf("%w: %s", errFakeSecretNotFound, name)
			return nil, rpcerr.New(connect.CodeNotFound, missing)
		}
		if len(secret.users) > 0 {
			used := fmt.Errorf("%w: %s", errFakeSecretInUse, strings.Join(secret.users, ", "))
			return nil, rpcerr.New(connect.CodeFailedPrecondition, used)
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
	f := s.api
	if err := f.authorizeSecrets(
		ctx, secretv1connect.SecretServiceListSecretsProcedure, req.GetEnvironmentId(),
	); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	updated := timestamppb.New(time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC))
	var secrets []*secretv1.Secret
	for key, secret := range f.platform.secrets {
		if key.environmentID != req.GetEnvironmentId() {
			continue
		}
		secrets = append(secrets, &secretv1.Secret{
			Name:          key.name,
			Version:       secret.version,
			UpdatedByType: fakeSecretUpdatedByType,
			UpdatedById:   fakeSecretUpdatedByID,
			CreatedAt:     updated,
			UpdatedAt:     updated,
			UsedBy:        f.platform.secretUsers(secret.users),
		})
	}
	slices.SortFunc(secrets, func(a, b *secretv1.Secret) int { return cmp.Compare(a.GetName(), b.GetName()) })
	return &secretv1.ListSecretsResponse{Secrets: secrets}, nil
}

func (f *fakeAPI) authorizeSecrets(ctx context.Context, procedure, environmentID string) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	if _, _, err := f.authenticate(connect.Spec{Procedure: procedure}, info.RequestHeader()); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.ContainsFunc(f.platform.environments, func(env *environmentv1.Environment) bool {
		return env.GetId() == environmentID
	}) {
		return rpcerr.New(connect.CodeNotFound, errFakeUnknownEnvironment)
	}
	return nil
}

func (p *fakePlatform) secretUsers(names []string) []*secretv1.SecretUser {
	users := make([]*secretv1.SecretUser, 0, len(names))
	for _, name := range names {
		id, _ := p.resourceID(name)
		users = append(users, &secretv1.SecretUser{ResourceId: id, ResourceSlug: name})
	}
	return users
}

func (p *fakePlatform) secretKey(ts *testscript.TestScript, environment, name string) fakeSecretKey {
	id, ok := p.environmentID(environment)
	if !ok {
		ts.Fatalf("fakeapi: no environment %s", environment)
	}
	return fakeSecretKey{environmentID: id, name: name}
}

// cmdFakeSecrets runs "fakeapi secret <env> <name> <value>", which checks a stored value, and
// "fakeapi secret-usage <env> <name> [service...]", which records the services reading a secret.
func cmdFakeSecrets(ts *testscript.TestScript, api *fakeAPI, neg bool, args []string) {
	if len(args) < 3 || (args[0] == "secret" && len(args) != 4) {
		ts.Fatalf("usage: fakeapi secret <env> <name> <value> | fakeapi secret-usage <env> <name> [service...]")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	environment, name := args[1], args[2]
	key := api.platform.secretKey(ts, environment, name)
	secret, ok := api.platform.secrets[key]
	if args[0] == "secret-usage" {
		if !ok {
			ts.Fatalf("fakeapi: secret %s of %s is not set", name, environment)
		}
		secret.users = args[3:]
		api.platform.secrets[key] = secret
		return
	}
	if neg {
		if ok {
			ts.Fatalf("fakeapi: secret %s of %s is set", name, environment)
		}
		return
	}
	if !ok {
		ts.Fatalf("fakeapi: secret %s of %s is not set", name, environment)
	}
	if secret.value != args[3] {
		ts.Fatalf("fakeapi: secret %s of %s has a different value than expected", name, environment)
	}
}
