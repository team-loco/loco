package platform

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
	"path/filepath"
	"strings"
	"time"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	fixtureClient   = "loco-e2e"
	fixturePassword = "carolpw"
	// fixtureUser is the application user the identity provider fixture lists.
	fixtureUser = "e2e@test.local"
)

var errProvider = errors.New("fixture identity provider sign-in failed")

// session is a CLI session the API issued, in the CLI's credentials file format
// (cmd/internal/keychain.UserToken).
type session struct {
	Host         string    `json:"host"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refreshToken"`
}

// signIn signs the configured admin and the application user in through the identity provider and
// the API's CLI login, so the run holds API-issued sessions, not rows inserted into the database.
func (p *Platform) signIn(ctx context.Context) error {
	admin, err := p.cliSession(ctx, SystemAdmin)
	if err != nil {
		return err
	}
	user, err := p.cliSession(ctx, fixtureUser)
	if err != nil {
		return err
	}
	p.adminSession, p.userSession = admin, user
	p.AdminToken, p.UserToken = admin.Token, user.Token
	p.Admin = rpc.ForHost(p.APIURL, admin.Token)
	p.User = rpc.ForHost(p.APIURL, user.Token)
	me, err := p.User.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		return err
	}
	p.UserID = me.GetUser().GetId()
	return writeCredentials(p.adminHome, admin)
}

func (p *Platform) cliSession(ctx context.Context, email string) (session, error) {
	proof, err := p.providerLogin(ctx, email)
	if err != nil {
		return session{}, err
	}
	clients := rpc.ForHost(p.APIURL, proof)
	verifier := bearer.New("")
	sum := sha256.Sum256([]byte(verifier))
	approved, err := clients.Auth.ApproveCLILogin(ctx, &authv1.ApproveCLILoginRequest{
		State:         bearer.New(""),
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	})
	if err != nil {
		return session{}, err
	}
	exchanged, err := clients.Auth.ExchangeCLICode(
		ctx,
		&authv1.ExchangeCLICodeRequest{Code: approved.GetCode(), CodeVerifier: verifier},
	)
	if err != nil {
		return session{}, err
	}
	tokens := exchanged.GetTokens()
	return session{
		Host:         p.APIURL,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.GetExpiresIn()) * time.Second),
		Token:        tokens.GetAccessToken(),
		RefreshToken: tokens.GetRefreshToken(),
	}, nil
}

func (p *Platform) providerLogin(ctx context.Context, email string) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {fixtureClient},
		"username":   {email},
		"password":   {fixturePassword},
		"scope":      {"openid email profile"},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.authIssuer()+"/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w for %s: HTTP %d", errProvider, email, response.StatusCode)
	}
	var token struct {
		Identity string `json:"id_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		return "", fmt.Errorf("%w for %s: %w", errProvider, email, err)
	}
	if token.Identity == "" {
		return "", fmt.Errorf("%w for %s: no ID token", errProvider, email)
	}
	return token.Identity, nil
}

func writeCredentials(home string, credentials session) error {
	if err := os.MkdirAll(filepath.Join(home, ".loco"), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, ".loco", "credentials.json"), data, 0o600)
}

// enrollCluster creates the run's cluster record as the system admin, and starts the agent with
// its join token.
func (p *Platform) enrollCluster(ctx context.Context) error {
	var clusters int
	if err := p.DB.QueryRow(ctx, "SELECT count(*) FROM clusters").Scan(&clusters); err != nil {
		return err
	}
	if clusters != 0 {
		return fmt.Errorf("the cluster registry holds %d clusters before enrollment", clusters)
	}
	out, err := p.Locoadm(
		ctx,
		"cluster",
		"create",
		"--name",
		clusterName,
		"--region",
		clusterRegion,
	)
	if err != nil {
		return err
	}
	var created struct {
		ClusterID string `json:"clusterId"`
		JoinToken struct {
			Token string `json:"token"`
		} `json:"joinToken"`
	}
	if err = json.Unmarshal([]byte(out), &created); err != nil {
		return fmt.Errorf("decode locoadm cluster create: %w", err)
	}
	p.JoinToken = created.JoinToken.Token
	if err = p.DeleteAgentKey(ctx); err != nil {
		return err
	}
	key, err := p.Kubectl(ctx, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return err
	}
	p.ClusterKey = strings.TrimSpace(key)
	if err := p.StartAgent(ctx, p.APIURL, p.JoinToken); err != nil {
		return err
	}
	return p.DB.QueryRow(ctx, "SELECT id FROM clusters WHERE cluster_key = $1", p.ClusterKey).Scan(&p.ClusterID)
}

// DeleteAgentKey deletes the agent's key Secret, so the agent enrolls with a new key.
func (p *Platform) DeleteAgentKey(ctx context.Context) error {
	_, err := p.Kubectl(
		ctx,
		"-n",
		locoNamespace,
		"delete",
		"secret",
		p.agentVariable("LOCO_AGENT_KEY_SECRET"),
		"--ignore-not-found",
	)
	return err
}

// Locoadm runs locoadm as the system admin.
func (p *Platform) Locoadm(ctx context.Context, args ...string) (string, error) {
	cmd := append(
		[]string{
			"HOME=" + p.adminHome,
			"LOCO_CREDENTIAL_STORE=file",
			"LOCO_HOST=" + p.APIURL,
			filepath.Join(p.binDir, "locoadm"),
		},
		args...)
	return p.Command(ctx, "env", cmd...)
}
