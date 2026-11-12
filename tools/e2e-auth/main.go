package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	fixtureClient   = "loco-e2e"
	setupMode       = "setup"
	fixturePassword = "carolpw"
	fixtureTimeout  = time.Minute
)

var (
	errProvider              = errors.New("fixture identity provider sign-in failed")
	errMode                  = errors.New("unknown authentication fixture mode")
	errUnexpectedBootstrap   = errors.New("bootstrap unexpectedly succeeded")
	errUnexpectedInvitedUser = errors.New("invited sign-in resolved another user")
	errEnvironmentMissing    = errors.New("new workspace has no production environment")
)

type config struct{ api, issuer, bootstrapToken, state string }

// Credential JSON follows cmd/internal/keychain.UserToken; these values are issued
// by the API, rather than invented sessions inserted into the fixture database.
type credentials struct {
	Host         string    `json:"host"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refreshToken"`
}
type state struct {
	UserID           string      `json:"userId"`
	OrgID            string      `json:"orgId"`
	WorkspaceID      string      `json:"workspaceId"`
	EnvironmentID    string      `json:"environmentId"`
	AdminCredentials credentials `json:"adminCredentials"`
	UserCredentials  credentials `json:"userCredentials"`
}

func main() {
	cfg := config{
		api:            os.Getenv("E2E_API_URL"),
		issuer:         os.Getenv("E2E_AUTH_ISSUER"),
		bootstrapToken: os.Getenv("E2E_BOOTSTRAP_TOKEN"),
		state:          os.Getenv("E2E_AUTH_STATE"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	mode := setupMode
	if len(os.Args) == 2 {
		mode = os.Args[1]
	}
	if err := run(ctx, cfg, mode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config, mode string) error {
	if mode == "invite" {
		return inviteUser(ctx, cfg)
	}
	proof, err := providerLogin(ctx, cfg.issuer, "loco-admin@test.local")
	if err != nil {
		return err
	}
	admin := rpc.ForHost(cfg.api, proof)
	token := cfg.bootstrapToken
	switch mode {
	case "wrong-token":
		token = "invalid-" + token
	case "closed":
	case setupMode:
	default:
		return errMode
	}
	response, err := admin.Bootstrap.Bootstrap(ctx, &bootstrapv1.BootstrapRequest{Token: token})
	if err != nil {
		return err
	}
	if mode != setupMode {
		return errUnexpectedBootstrap
	}
	userProof, err := providerLogin(ctx, cfg.issuer, "e2e@test.local")
	if err != nil {
		return err
	}
	user := rpc.ForHost(cfg.api, userProof)
	me, err := user.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		return err
	}
	userTokens, err := session(ctx, user)
	if err != nil {
		return err
	}
	org, err := user.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new("e2e-test-org")})
	if err != nil {
		return err
	}
	workspace, err := user.Workspace.CreateWorkspace(
		ctx,
		&workspacev1.CreateWorkspaceRequest{OrgId: org.GetOrgId(), Name: "e2e-test-workspace"},
	)
	if err != nil {
		return err
	}
	environments, err := user.Environment.ListEnvironments(
		ctx,
		&environmentv1.ListEnvironmentsRequest{WorkspaceId: workspace.GetWorkspaceId()},
	)
	if err != nil {
		return err
	}
	production := ""
	for _, environment := range environments.GetEnvironments() {
		if environment.GetType() == environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION {
			production = environment.GetId()
		}
	}
	if production == "" {
		return errEnvironmentMissing
	}
	result := state{
		UserID:           me.GetUser().GetId(),
		OrgID:            org.GetOrgId(),
		WorkspaceID:      workspace.GetWorkspaceId(),
		EnvironmentID:    production,
		AdminCredentials: credential(cfg.api, response.GetTokens()),
		UserCredentials:  credential(cfg.api, userTokens),
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode fixture state: %w", err)
	}
	if err = os.WriteFile(cfg.state, data, 0600); err != nil {
		return fmt.Errorf("write fixture state: %w", err)
	}
	return nil
}

func inviteUser(ctx context.Context, cfg config) error {
	data, err := os.ReadFile(cfg.state)
	if err != nil {
		return fmt.Errorf("read fixture state: %w", err)
	}
	var saved state
	if decodeErr := json.Unmarshal(data, &saved); decodeErr != nil {
		return fmt.Errorf("decode fixture state: %w", decodeErr)
	}
	admin := rpc.ForHost(cfg.api, saved.AdminCredentials.Token)
	const email = "invited@test.local"
	created, err := admin.User.CreateUser(ctx, &userv1.CreateUserRequest{Email: email})
	if err != nil {
		return fmt.Errorf("invite user: %w", err)
	}
	proof, err := providerLogin(ctx, cfg.issuer, email)
	if err != nil {
		return err
	}
	user := rpc.ForHost(cfg.api, proof)
	me, err := user.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		return fmt.Errorf("invited sign-in: %w", err)
	}
	if me.GetUser().GetId() != created.GetUser().GetId() {
		return errUnexpectedInvitedUser
	}
	_, err = user.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new("invited-org")})
	return err
}

func providerLogin(ctx context.Context, issuer, email string) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {fixtureClient},
		"username":   {email},
		"password":   {fixturePassword},
		"scope":      {"openid email profile"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errProvider
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", errProvider
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errProvider
	}
	var token struct {
		Identity string `json:"id_token"`
	}
	if err = json.NewDecoder(response.Body).Decode(&token); err != nil || token.Identity == "" {
		return "", errProvider
	}
	return token.Identity, nil
}

func session(ctx context.Context, clients *rpc.Clients) (*authv1.CLITokens, error) {
	verifier := bearer.New("")
	sum := sha256.Sum256([]byte(verifier))
	approved, err := clients.Auth.ApproveCLILogin(
		ctx,
		&authv1.ApproveCLILoginRequest{
			State:         bearer.New(""),
			CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		},
	)
	if err != nil {
		return nil, err
	}
	exchanged, err := clients.Auth.ExchangeCLICode(
		ctx,
		&authv1.ExchangeCLICodeRequest{Code: approved.GetCode(), CodeVerifier: verifier},
	)
	if err != nil {
		return nil, err
	}
	return exchanged.GetTokens(), nil
}

func credential(host string, tokens *authv1.CLITokens) credentials {
	return credentials{
		Host:         host,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.GetExpiresIn()) * time.Second),
		Token:        tokens.GetAccessToken(),
		RefreshToken: tokens.GetRefreshToken(),
	}
}
