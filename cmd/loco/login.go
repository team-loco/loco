package loco

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	oAuth "github.com/team-loco/loco/gen/go/loco/oauth/v1"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/api"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

const contentTypeJSON = "application/json"

type DeviceCodeRequest struct {
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
}

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type AuthTokenRequest struct {
	ClientID   string `json:"client_id"`
	DeviceCode string `json:"device_code"`
	GrantType  string `json:"grant_type"`
}

type AuthTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type TokenDetails struct {
	ClientID string  `json:"clientId"`
	TokenTTL float64 `json:"tokenTTL"`
}

func newLoginCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login to loco via Github OAuth",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			store, err := env.Tokens()
			if err != nil {
				return err
			}

			t, err := store.Get()
			if err != nil && !errors.Is(err, keychain.ErrNotFound) {
				slog.Error("failed keychain token grab", "error", err)
			}

			if err == nil && t.Host != host {
				slog.Debug("stored token belongs to another host", "token_host", t.Host, "host", host)
			}
			if err == nil && t.Host == host {
				if !t.ExpiresAt.Before(time.Now().Add(1 * time.Hour)) {
					checkmark := lipgloss.NewStyle().Foreground(ui.Ok).Render("✔")
					message := lipgloss.NewStyle().Bold(true).Foreground(ui.Accent).Render("Already logged in!")
					subtext := lipgloss.NewStyle().
						Foreground(ui.Fg2).
						Render("You can continue using loco")

					fmt.Printf("%s %s\n%s\n", checkmark, message, subtext)
					return nil
				}
				slog.Debug("token is expired or will expire soon", "expires_at", t.ExpiresAt)
			} else {
				slog.Debug("no token found in keychain", "error", err)
			}
			c := api.NewClient("https://github.com")

			httpClient := httputil.NewHTTPClient()
			oAuthClient := oauthv1connect.NewOAuthServiceClient(httpClient, host)
			resp, err := oAuthClient.GetOAuthDetails(ctx, connect.NewRequest(&oAuth.GetOAuthDetailsRequest{
				Provider: oAuth.OAuthProvider_O_AUTH_PROVIDER_GITHUB,
			}))
			if err != nil {
				cmdutil.LogRequestID(ctx, err, "failed to get oAuth details")
				return fmt.Errorf("login to %s failed: %w", host, err)
			}
			slog.Debug("retrieved oauth details", "client_id", resp.Msg.GetClientId())

			payload := DeviceCodeRequest{
				ClientID: resp.Msg.GetClientId(),
				Scope:    "read:user user:email",
			}

			req, err := c.Post(ctx, "/login/device/code", payload, map[string]string{
				"Accept":       contentTypeJSON,
				"Content-Type": contentTypeJSON,
			})
			if err != nil {
				slog.Debug("failed to get device code", "error", err)
				return err
			}

			deviceTokenResponse := new(DeviceCodeResponse)
			err = json.Unmarshal(req, deviceTokenResponse)
			if err != nil {
				slog.Debug("failed to unmarshal device code response", "error", err)
				return err
			}

			tokenChan := make(chan AuthTokenResponse, 1)
			errorChan := make(chan error, 1)

			pollCtx, cancelPoll := context.WithCancel(ctx)
			defer cancelPoll()
			pollInterval := time.Duration(deviceTokenResponse.Interval) * time.Second

			go func() {
				token, pollErr := pollAuthToken(
					pollCtx,
					c,
					payload.ClientID,
					deviceTokenResponse.DeviceCode,
					pollInterval,
				)
				if pollErr != nil {
					errorChan <- pollErr
					return
				}
				tokenChan <- *token
			}()

			m := initialModel(deviceTokenResponse.UserCode, deviceTokenResponse.VerificationURI, tokenChan, errorChan)
			p := tea.NewProgram(m)

			fm, err := p.Run()
			if err != nil {
				return err
			}

			finalM, ok := fm.(model)
			if !ok {
				return fmt.Errorf("%w: unexpected model type", ErrCommandFailed)
			}

			if finalM.err != nil {
				return finalM.err
			}

			if finalM.tokenResp != nil {
				slog.Debug("received auth token from github oauth")
			}

			if finalM.tokenResp == nil {
				return errors.New("login canceled")
			}

			locoResp, err := oAuthClient.ExchangeOAuthToken(
				ctx,
				connect.NewRequest(&oAuth.ExchangeOAuthTokenRequest{
					Provider:              oAuth.OAuthProvider_O_AUTH_PROVIDER_GITHUB,
					Token:                 finalM.tokenResp.AccessToken,
					CreateUserIfNotExists: true,
				}),
			)
			if err != nil {
				cmdutil.LogRequestID(ctx, err, "failed to exchange oauth token")
				return fmt.Errorf("login to %s failed: %w", host, err)
			}

			if err := setupLoginScope(ctx, httpClient, host, store, locoResp.Msg); err != nil {
				return fmt.Errorf("login to %s failed: %w", host, err)
			}
			return nil
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	return cmd
}

func setupLoginScope(
	ctx context.Context,
	httpClient *http.Client,
	host string,
	store keychain.TokenStore,
	exchange *oAuth.ExchangeOAuthTokenResponse,
) error {
	newToken := tokenFromExchange(host, exchange)

	cfg, err := session.Load()
	if err != nil {
		slog.Debug("failed to load existing config", "error", err)
		cfg = session.NewSessionConfig()
	}

	if cfg.LocoHost == host {
		scope, scopeErr := cfg.GetScope()
		if scopeErr == nil {
			if storeErr := store.Set(newToken); storeErr != nil {
				return fmt.Errorf("failed to store token: %w", storeErr)
			}
			printLoginSuccess("Logged in!", scope.Organization.Name, scope.Workspace.Name)
			return nil
		}
	}

	org, workspace, err := resolveLoginScope(ctx, httpClient, host, exchange.GetLocoToken())
	if err != nil {
		return err
	}

	cfg.LocoHost = host
	cfg.Scopes = make(map[string]*session.Scope)
	if err := cfg.SetDefaultScope(org, workspace); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if storeErr := store.Set(newToken); storeErr != nil {
		return fmt.Errorf("failed to store token: %w", storeErr)
	}

	printLoginSuccess("Authentication successful!", org.Name, workspace.Name)
	return nil
}

func resolveLoginScope(
	ctx context.Context,
	httpClient *http.Client,
	host string,
	locoToken string,
) (session.SimpleOrg, session.SimpleWorkspace, error) {
	authorization := fmt.Sprintf("Bearer %s", locoToken)
	orgClient := orgv1connect.NewOrgServiceClient(httpClient, host)
	wsClient := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)
	userClient := userv1connect.NewUserServiceClient(httpClient, host)

	currentUserReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	currentUserReq.Header().Set("Authorization", authorization)
	currentUserResp, err := userClient.WhoAmI(ctx, currentUserReq)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to get current user: %w", err)
	}

	orgRequest := connect.NewRequest(&orgv1.ListUserOrgsRequest{
		UserId:   currentUserResp.Msg.GetUser().GetId(),
		PageSize: 100,
	})
	orgRequest.Header().Set("Authorization", authorization)
	orgResp, err := orgClient.ListUserOrgs(ctx, orgRequest)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to list organizations: %w", err)
	}

	orgs := orgResp.Msg.GetOrgs()
	if len(orgs) > 0 {
		selectedOrg := orgs[0]
		wsReq := connect.NewRequest(&workspacev1.ListOrgWorkspacesRequest{
			OrgId:    selectedOrg.GetId(),
			PageSize: 100,
		})
		wsReq.Header().Set("Authorization", authorization)
		wsResp, wsErr := wsClient.ListOrgWorkspaces(ctx, wsReq)
		if wsErr != nil {
			return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to list workspaces: %w", wsErr)
		}
		workspaces := wsResp.Msg.GetWorkspaces()
		if len(workspaces) == 0 {
			return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf(
				"organization %q has no workspaces",
				selectedOrg.GetName(),
			)
		}
		org := session.SimpleOrg{ID: selectedOrg.GetId(), Name: selectedOrg.GetName()}
		workspace := session.SimpleWorkspace{ID: workspaces[0].GetId(), Name: workspaces[0].GetName()}
		return org, workspace, nil
	}

	email := currentUserResp.Msg.GetUser().GetEmail()
	orgName := fmt.Sprintf("%s-org", cleanEmail(email))
	createOrgReq := connect.NewRequest(&orgv1.CreateOrgRequest{Name: &orgName})
	createOrgReq.Header().Set("Authorization", authorization)
	createOrgResp, err := orgClient.CreateOrg(ctx, createOrgReq)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to create organization: %w", err)
	}

	getOrgReq := connect.NewRequest(&orgv1.GetOrgRequest{
		Key: &orgv1.GetOrgRequest_OrgId{OrgId: createOrgResp.Msg.GetOrgId()},
	})
	getOrgReq.Header().Set("Authorization", authorization)
	getOrgResp, err := orgClient.GetOrg(ctx, getOrgReq)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf(
			"failed to get created organization: %w",
			err,
		)
	}

	createWSReq := connect.NewRequest(&workspacev1.CreateWorkspaceRequest{
		OrgId: createOrgResp.Msg.GetOrgId(),
		Name:  "default",
	})
	createWSReq.Header().Set("Authorization", authorization)
	createWSResp, err := wsClient.CreateWorkspace(ctx, createWSReq)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to create workspace: %w", err)
	}

	getWSReq := connect.NewRequest(&workspacev1.GetWorkspaceRequest{
		WorkspaceId: createWSResp.Msg.GetWorkspaceId(),
	})
	getWSReq.Header().Set("Authorization", authorization)
	getWSResp, err := wsClient.GetWorkspace(ctx, getWSReq)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to get created workspace: %w", err)
	}

	org := session.SimpleOrg{
		ID:   getOrgResp.Msg.GetOrganization().GetId(),
		Name: getOrgResp.Msg.GetOrganization().GetName(),
	}
	workspace := session.SimpleWorkspace{
		ID:   getWSResp.Msg.GetWorkspace().GetId(),
		Name: getWSResp.Msg.GetWorkspace().GetName(),
	}
	return org, workspace, nil
}

func cleanEmail(email string) string {
	s := strings.ToLower(email)
	s = strings.ReplaceAll(s, "@", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "+", "-")
	return s
}

func printLoginSuccess(title, orgName, workspaceName string) {
	checkmark := lipgloss.NewStyle().Foreground(ui.Ok).Render("✔")
	heading := lipgloss.NewStyle().Bold(true).Foreground(ui.Accent).Render(title)
	orgLine := lipgloss.NewStyle().
		Foreground(ui.Fg2).
		Render(fmt.Sprintf("  Organization: %s", orgName))
	wsLine := lipgloss.NewStyle().
		Foreground(ui.Fg2).
		Render(fmt.Sprintf("  Workspace: %s", workspaceName))
	fmt.Printf("%s %s\n%s\n%s\n", checkmark, heading, orgLine, wsLine)
}

func tokenFromExchange(host string, resp *oAuth.ExchangeOAuthTokenResponse) keychain.UserToken {
	lifetime := time.Duration(resp.GetExpiresIn())*time.Second - 10*time.Minute
	expiresAt := time.Now().Add(lifetime)
	return keychain.UserToken{
		Host:         host,
		Token:        resp.GetLocoToken(),
		RefreshToken: resp.GetRefreshToken(),
		ExpiresAt:    expiresAt,
	}
}

func pollAuthToken(
	ctx context.Context,
	c *api.Client,
	clientID string,
	deviceCode string,
	interval time.Duration,
) (*AuthTokenResponse, error) {
	authTokenRequest := AuthTokenRequest{
		ClientID:   clientID,
		DeviceCode: deviceCode,
		GrantType:  "urn:ietf:params:oauth:grant-type:device_code",
	}
	headers := map[string]string{
		"Accept":       contentTypeJSON,
		"Content-Type": contentTypeJSON,
	}

	for {
		wait := time.After(interval)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wait:
		}

		resp, err := c.Post(ctx, "/login/oauth/access_token", authTokenRequest, headers)
		if err != nil {
			apiError, ok := errors.AsType[*api.APIError](err)
			if !ok {
				slog.Debug("network error while polling for token", "error", err)
				return nil, fmt.Errorf("network error: %w", err)
			}
			switch apiError.StatusCode {
			case http.StatusBadRequest:
				slog.Debug("authorization pending", "status_code", apiError.StatusCode)
				continue
			case http.StatusForbidden:
				slog.Debug("access denied or rate limited", "status_code", apiError.StatusCode, "error", err)
				return nil, fmt.Errorf("access denied or rate limited: %w", err)
			default:
				slog.Debug("API error while polling for token", "status_code", apiError.StatusCode, "error", err)
				return nil, fmt.Errorf("API error: %w", err)
			}
		}

		authTokenResponse := new(AuthTokenResponse)
		if err = json.Unmarshal(resp, authTokenResponse); err != nil {
			slog.Debug("failed to unmarshal auth token response", "error", err)
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		switch authTokenResponse.Error {
		case "":
			if authTokenResponse.AccessToken != "" {
				return authTokenResponse, nil
			}
		case "authorization_pending":
			slog.Debug("authorization pending")
		case "slow_down":
			interval += 5 * time.Second
			slog.Debug("github asked to slow down", "interval", interval)
		default:
			return nil, fmt.Errorf(
				"github device authorization failed: %s: %s",
				authTokenResponse.Error,
				authTokenResponse.ErrorDescription,
			)
		}
	}
}

type (
	tickMsg        time.Time
	authSuccessMsg struct {
		Token AuthTokenResponse
	}
	authErrorMsg struct {
		Error error
	}
)

func waitForToken(tokenChan <-chan AuthTokenResponse) tea.Cmd {
	return func() tea.Msg {
		token := <-tokenChan
		return authSuccessMsg{Token: token}
	}
}

func waitForError(errorChan <-chan error) tea.Cmd {
	return func() tea.Msg {
		err := <-errorChan
		return authErrorMsg{Error: err}
	}
}

type model struct {
	tokenResp       *AuthTokenResponse
	tokenChan       <-chan AuthTokenResponse
	errorChan       <-chan error
	loadingFrames   []string
	userCode        string
	verificationURI string
	err             error
	frameIndex      int
	polling         bool
	done            bool
}

func initialModel(
	userCode string,
	verificationURI string,
	tokenChan <-chan AuthTokenResponse,
	errorChan <-chan error,
) model {
	return model{
		userCode:        userCode,
		verificationURI: verificationURI,
		loadingFrames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		frameIndex:      0,
		polling:         true,
		done:            false,
		tokenChan:       tokenChan,
		errorChan:       errorChan,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tick(),
		waitForToken(m.tokenChan),
		waitForError(m.errorChan),
	)
}

func tick() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tickMsg:
		if m.polling && !m.done {
			m.frameIndex = (m.frameIndex + 1) % len(m.loadingFrames)
			return m, tick()
		}
	case authSuccessMsg:
		m.polling = false
		m.done = true
		m.tokenResp = &msg.Token
		return m, tea.Quit
	case authErrorMsg:
		m.polling = false
		m.done = true
		m.err = msg.Error
		return m, tea.Quit
	}

	return m, nil
}

func (m model) View() tea.View {
	if m.done {
		if m.err != nil {
			errorStyle := lipgloss.NewStyle().Foreground(ui.Bad).Bold(true)
			return tea.NewView(fmt.Sprintf("%s\n%s\n",
				errorStyle.Render("Authentication failed:"),
				lipgloss.NewStyle().Foreground(ui.Fg2).Render(m.err.Error())))
		}
		return tea.NewView(
			lipgloss.NewStyle().Foreground(ui.Fg2).Render("Setting up organization and workspace...") + "\n",
		)
	}

	codeStyle := lipgloss.NewStyle().Foreground(ui.Accent).Bold(true).Padding(0, 0)
	urlStyle := lipgloss.NewStyle().Foreground(ui.Link).Underline(true)
	instructionStyle := lipgloss.NewStyle().Foreground(ui.Fg2)
	spinnerStyle := lipgloss.NewStyle().Foreground(ui.Accent).Bold(true)

	spinner := ""
	if len(m.loadingFrames) > 0 {
		spinner = spinnerStyle.Render(m.loadingFrames[m.frameIndex])
	}

	return tea.NewView(fmt.Sprintf(
		"%s %s\n\n%s %s\n\n%s %s\n\n%s",
		instructionStyle.Render("Please open the following URL in your browser:"),
		urlStyle.Render(m.verificationURI),
		instructionStyle.Render("Then, enter the following user code:"),
		codeStyle.Render(m.userCode),
		spinner,
		instructionStyle.Render("Waiting for authentication..."),
		lipgloss.NewStyle().Foreground(ui.Fg3).Faint(true).Render("Press 'q' or Ctrl+C to quit"),
	))
}
