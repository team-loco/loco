package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/registrytoken"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/jwssign"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	RegistryPullUsername  = "loco-pull"
	RegistryBuildUsername = "loco-build"
	RegistryAPISubject    = "api"

	registryCredentialPull    = "pull"
	registryCredentialBuild   = "build"
	registryTokenFormMaxBytes = 64 * 1024
	registryGrantTypePassword = "password"
	registryTokenType         = "Bearer"
	registryChallengeFormat   = "Basic realm=%q"
	registryTokenContentType  = "application/json"
	registryTokenCacheControl = "no-store"
)

var (
	errRegistryNotTokenMode = errors.New(
		"the API does not issue registry credentials: LOCO_REGISTRY_AUTH is static",
	)
	errWorkspaceNotPlaced   = errors.New("the workspace has no placement on this cluster")
	errRegistryTokenRequest = errors.New("malformed registry token request")
	errRegistryService      = errors.New("service does not name this registry")
	errRegistryGrantType    = errors.New("grant_type must be password")
	errRegistryCredential   = errors.New("registry credential is not valid")

	errRegistryCredentialKind = errors.New("unknown registry credential kind")
)

var registryCredentialKinds = map[string]string{
	RegistryPullUsername:  registryCredentialPull,
	RegistryBuildUsername: registryCredentialBuild,
}

type RegistryConfig struct {
	TokenMode              bool
	Service                string
	Issuer                 string
	RegistryHost           string
	RegistryPrefix         string
	AccessTokenLifetime    time.Duration
	PullCredentialLifetime time.Duration
}

type RegistryServer struct {
	queries  genDb.Querier
	clusters *ClusterAuthenticator
	signer   jwssign.Signer
	keyID    string
	config   RegistryConfig
	now      func() time.Time
}

func NewRegistryServer(
	queries genDb.Querier,
	clusters *ClusterAuthenticator,
	signer jwssign.Signer,
	keyID string,
	config RegistryConfig,
) *RegistryServer {
	return &RegistryServer{
		queries:  queries,
		clusters: clusters,
		signer:   signer,
		keyID:    keyID,
		config:   config,
		now:      time.Now,
	}
}

func (s *RegistryServer) IssuePullCredential(
	ctx context.Context,
	req *registryv1.IssuePullCredentialRequest,
) (*registryv1.IssuePullCredentialResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	cluster, err := s.clusters.Authenticate(ctx, info.RequestHeader().Get("Authorization"))
	if err != nil {
		return nil, agentAuthError(err)
	}
	if !s.config.TokenMode {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, errRegistryNotTokenMode)
	}
	workspaceID, err := uuid.Parse(req.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidWorkspaceID).WithCause(err)
	}
	password, hash := newRegistrySecret()
	expiresAt := s.now().Add(s.config.PullCredentialLifetime)
	created, err := s.queries.CreatePullRegistryCredential(ctx, genDb.CreatePullRegistryCredentialParams{
		ClusterID:   cluster.ID,
		WorkspaceID: workspaceID,
		SecretHash:  hash,
		ExpiresAt:   &expiresAt,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	if created == 0 {
		return nil, rpcerr.New(connect.CodePermissionDenied, errWorkspaceNotPlaced)
	}
	expires := timestamppb.New(expiresAt)
	return &registryv1.IssuePullCredentialResponse{
		Username:  RegistryPullUsername,
		Password:  password,
		ExpiresAt: expires,
	}, nil
}

func newRegistrySecret() (string, []byte) {
	secret := bearer.New("")
	return secret, registrySecretHash(secret)
}

func registrySecretHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

type registryTokenRequest struct {
	username string
	password string
	scopes   []registrytoken.Access
	form     bool
}

type registryTokenResponse struct {
	Token       string    `json:"token"`
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
}

type registryOAuthTokenResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
}

func (s *RegistryServer) Token(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	request, err := s.readTokenRequest(w, r)
	if err != nil {
		message := err.Error()
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	kind, ok := registryCredentialKinds[request.username]
	if !ok || request.password == "" {
		s.refuseCredential(w)
		return
	}
	hash := registrySecretHash(request.password)
	grant, err := s.queries.GetRegistryGrant(ctx, genDb.GetRegistryGrantParams{SecretHash: hash, Kind: kind})
	if errors.Is(err, pgx.ErrNoRows) {
		s.refuseCredential(w)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "look up registry credential", "error", err)
		http.Error(w, publicRequestFailed, http.StatusServiceUnavailable)
		return
	}
	access := s.grantedAccess(ctx, grant, request.scopes)
	now := s.now().Truncate(time.Second)
	subject := grant.ClusterID.String()
	token, err := s.sign(ctx, subject, grant.BuildID, access, now)
	if err != nil {
		slog.ErrorContext(ctx, "sign registry access token", "error", err)
		http.Error(w, publicRequestFailed, http.StatusServiceUnavailable)
		return
	}
	expiresIn := int(s.config.AccessTokenLifetime / time.Second)
	issuedAt := now.UTC()
	var body any = registryTokenResponse{Token: token, AccessToken: token, ExpiresIn: expiresIn, IssuedAt: issuedAt}
	if request.form {
		body = registryOAuthTokenResponse{
			AccessToken: token,
			TokenType:   registryTokenType,
			ExpiresIn:   expiresIn,
			IssuedAt:    issuedAt,
		}
	}
	w.Header().Set("Content-Type", registryTokenContentType)
	w.Header().Set("Cache-Control", registryTokenCacheControl)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.WarnContext(ctx, "write registry access token", "error", err)
	}
}

func (s *RegistryServer) readTokenRequest(w http.ResponseWriter, r *http.Request) (registryTokenRequest, error) {
	var request registryTokenRequest
	var values url.Values
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, registryTokenFormMaxBytes)
		if err := r.ParseForm(); err != nil {
			return request, fmt.Errorf("%w: %w", errRegistryTokenRequest, err)
		}
		values = r.PostForm
		if values.Get("grant_type") != registryGrantTypePassword {
			return request, errRegistryGrantType
		}
		request.username = values.Get("username")
		request.password = values.Get("password")
		request.form = true
	} else {
		values = r.URL.Query()
		request.username, request.password, _ = r.BasicAuth()
	}
	if values.Get("service") != s.config.Service {
		return request, errRegistryService
	}
	scopes, err := registrytoken.ParseScopes(values["scope"])
	if err != nil {
		return request, fmt.Errorf("%w: %w", errRegistryTokenRequest, err)
	}
	request.scopes = scopes
	return request, nil
}

func (s *RegistryServer) refuseCredential(w http.ResponseWriter) {
	challenge := fmt.Sprintf(registryChallengeFormat, s.config.Service)
	w.Header().Set("WWW-Authenticate", challenge)
	message := errRegistryCredential.Error()
	http.Error(w, message, http.StatusUnauthorized)
}

func (s *RegistryServer) grantedAccess(
	ctx context.Context,
	grant genDb.GetRegistryGrantRow,
	requested []registrytoken.Access,
) []registrytoken.Access {
	granted := []registrytoken.Access{}
	for _, scope := range requested {
		if !registrytoken.ValidRepository(scope.Name) {
			continue
		}
		var actions []string
		for _, action := range s.allowedActions(ctx, grant, scope.Name) {
			if slices.Contains(scope.Actions, action) {
				actions = append(actions, action)
			}
		}
		if len(actions) == 0 {
			continue
		}
		granted = append(granted, registrytoken.Access{Type: scope.Type, Name: scope.Name, Actions: actions})
	}
	return granted
}

func (s *RegistryServer) allowedActions(
	ctx context.Context,
	grant genDb.GetRegistryGrantRow,
	repository string,
) []string {
	switch grant.Kind {
	case registryCredentialPull:
		prefix := workspaceRepositoryPrefix(s.config.RegistryPrefix, grant.WorkspaceID)
		if strings.HasPrefix(repository, prefix) {
			return []string{registrytoken.ActionPull}
		}
		return nil
	case registryCredentialBuild:
		buildRepository, err := registryPath(s.config.RegistryHost, *grant.ImageRepository)
		if err != nil {
			slog.ErrorContext(ctx, "build repository is not on the registry", "buildId", grant.BuildID, "error", err)
			return nil
		}
		if repository == buildRepository {
			return []string{registrytoken.ActionPull, registrytoken.ActionPush}
		}
		return nil
	default:
		panic(fmt.Errorf("%w: %q", errRegistryCredentialKind, grant.Kind))
	}
}

func (s *RegistryServer) SignAPIToken(ctx context.Context, access []registrytoken.Access) (string, error) {
	now := s.now().Truncate(time.Second)
	return s.sign(ctx, RegistryAPISubject, nil, access, now)
}

func (s *RegistryServer) sign(
	ctx context.Context,
	subject string,
	buildID *uuid.UUID,
	access []registrytoken.Access,
	now time.Time,
) (string, error) {
	issuedAt := jwt.NewNumericDate(now)
	expiresAt := now.Add(s.config.AccessTokenLifetime)
	expiry := jwt.NewNumericDate(expiresAt)
	tokenID := rand.Text()
	claims := registrytoken.Claims{
		Issuer:    s.config.Issuer,
		Subject:   subject,
		Audience:  jwt.Audience{s.config.Service},
		IssuedAt:  issuedAt,
		NotBefore: issuedAt,
		Expiry:    expiry,
		ID:        tokenID,
		Access:    access,
	}
	if buildID != nil {
		claims.Build = buildID.String()
	}
	return registrytoken.Sign(ctx, s.signer, s.keyID, claims)
}
