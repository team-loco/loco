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
			if err != nil {
				slog.Error("failed keychain token grab", "error", err)
			}

			if err == nil {
				if !t.ExpiresAt.Before(time.Now().Add(1 * time.Hour)) {
					checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
					message := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Already logged in!")
					subtext := lipgloss.NewStyle().
						Foreground(ui.LocoLightGray).
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
				return err
			}
			slog.Debug("retrieved oauth details", "client_id", resp.Msg.ClientId)

			payload := DeviceCodeRequest{
				ClientID: resp.Msg.ClientId,
				Scope:    "read:user user:email",
			}

			req, err := c.Post("/login/device/code", payload, map[string]string{
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
				return nil
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
				return err
			}

			return setupLoginScope(ctx, httpClient, host, store, locoResp.Msg)
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
	newToken := tokenFromExchange(exchange)
	orgClient := orgv1connect.NewOrgServiceClient(httpClient, host)
	wsClient := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)

	existingCfg, err := session.Load()
	if err != nil {
		slog.Debug("failed to load existing config", "error", err)
	}

	// use existing scope if it exists.
	if existingCfg != nil {
		scope, scopeErr := existingCfg.GetScope()
		if scopeErr == nil {
			if storeErr := store.Set(newToken); storeErr != nil {
				return fmt.Errorf("failed to store token: %w", storeErr)
			}

			checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
			title := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Logged in!")
			orgLine := lipgloss.NewStyle().
				Foreground(ui.LocoLightGray).
				Render(fmt.Sprintf("  Organization: %s", scope.Organization.Name))
			wsLine := lipgloss.NewStyle().
				Foreground(ui.LocoLightGray).
				Render(fmt.Sprintf("  Workspace: %s", scope.Workspace.Name))
			fmt.Printf("%s %s\n%s\n%s\n", checkmark, title, orgLine, wsLine)
			return nil
		}
	}

	var selectedOrg *orgv1.Organization
	var selectedWorkspace *workspacev1.Workspace

	userClient := userv1connect.NewUserServiceClient(httpClient, host)

	currentUserReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	currentUserReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

	currentUserResp, err := userClient.WhoAmI(ctx, currentUserReq)
	if err != nil {
		slog.Debug("failed to get current user", "error", err)
		return fmt.Errorf("failed to get current user: %w", err)
	}

	orgRequest := connect.NewRequest(&orgv1.ListUserOrgsRequest{
		UserId:   currentUserResp.Msg.User.Id,
		PageSize: 100,
	})
	orgRequest.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

	orgResp, err := orgClient.ListUserOrgs(ctx, orgRequest)
	if err != nil {
		slog.Debug("failed to get user orgs details", "error", err)
		return err
	}

	email := currentUserResp.Msg.User.GetEmail()
	cleanEmailFunc := func(email string) string {
		s := strings.ToLower(email)
		s = strings.ReplaceAll(s, "@", "-")
		s = strings.ReplaceAll(s, ".", "-")
		s = strings.ReplaceAll(s, "+", "-")
		return s
	}
	cleanedEmail := cleanEmailFunc(email)

	orgs := orgResp.Msg.GetOrgs()
	if len(orgs) == 0 {
		orgName := fmt.Sprintf("%s-org", cleanedEmail)
		createOrgReq := connect.NewRequest(&orgv1.CreateOrgRequest{
			Name: &orgName,
		})
		createOrgReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

		createOrgResp, err := orgClient.CreateOrg(ctx, createOrgReq)
		if err != nil {
			slog.Debug("failed to create organization", "error", err)
			return fmt.Errorf("failed to create organization: %w", err)
		}

		createdOrg := createOrgResp.Msg
		if createdOrg == nil {
			return fmt.Errorf("organization creation returned empty response")
		}

		// Fetch the created org to get its name
		getOrgReq := connect.NewRequest(&orgv1.GetOrgRequest{
			Key: &orgv1.GetOrgRequest_OrgId{
				OrgId: createdOrg.OrgId,
			},
		})
		getOrgReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

		getOrgResp, err := orgClient.GetOrg(ctx, getOrgReq)
		if err != nil {
			slog.Debug("failed to get created organization", "error", err)
			return fmt.Errorf("failed to get created organization: %w", err)
		}

		workspaceName := "default"
		wsClientNew := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)
		createWSReq := connect.NewRequest(&workspacev1.CreateWorkspaceRequest{
			OrgId: createdOrg.OrgId,
			Name:  workspaceName,
		})
		createWSReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

		createWSResp, err := wsClientNew.CreateWorkspace(ctx, createWSReq)
		if err != nil {
			slog.Debug("failed to create workspace", "error", err)
			return fmt.Errorf("failed to create workspace: %w", err)
		}

		// Fetch the created workspace to get its name
		getWSReq := connect.NewRequest(&workspacev1.GetWorkspaceRequest{
			WorkspaceId: createWSResp.Msg.WorkspaceId,
		})
		getWSReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

		getWSResp, err := wsClientNew.GetWorkspace(ctx, getWSReq)
		if err != nil {
			slog.Debug("failed to get created workspace", "error", err)
			return fmt.Errorf("failed to get created workspace: %w", err)
		}

		cfg := session.NewSessionConfig()
		if err := cfg.SetDefaultScope(
			session.SimpleOrg{ID: getOrgResp.Msg.Organization.Id, Name: getOrgResp.Msg.Organization.Name},
			session.SimpleWorkspace{ID: getWSResp.Msg.Workspace.Id, Name: getWSResp.Msg.Workspace.Name},
		); err != nil {
			slog.Error(err.Error())
			return err
		}

		if storeErr := store.Set(newToken); storeErr != nil {
			return fmt.Errorf("failed to store token: %w", storeErr)
		}

		checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
		title := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Authentication successful!")
		orgLine := lipgloss.NewStyle().
			Foreground(ui.LocoLightGray).
			Render(fmt.Sprintf("  Organization: %s", getOrgResp.Msg.Organization.Name))
		wsLine := lipgloss.NewStyle().
			Foreground(ui.LocoLightGray).
			Render(fmt.Sprintf("  Workspace: %s", getWSResp.Msg.Workspace.Name))
		fmt.Printf("%s %s\n%s\n%s\n", checkmark, title, orgLine, wsLine)

		return nil
	}

	if len(orgs) == 1 {
		selectedOrg = orgs[0]

		wsReq := connect.NewRequest(&workspacev1.ListOrgWorkspacesRequest{
			OrgId:    selectedOrg.Id,
			PageSize: 100,
		})
		wsReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

		wsResp, err := wsClient.ListOrgWorkspaces(ctx, wsReq)
		if err != nil {
			slog.Debug("failed to get workspaces for org", "orgId", selectedOrg.Id, "error", err)
			return fmt.Errorf("failed to list workspaces: %w", err)
		}

		workspaces := wsResp.Msg.Workspaces
		if len(workspaces) == 0 {
			return fmt.Errorf("organization has no workspaces")
		}

		selectedWorkspace = workspaces[0]
	} else {
		selectedOrg = orgs[0]

		wsReq := connect.NewRequest(&workspacev1.ListOrgWorkspacesRequest{
			OrgId:    selectedOrg.Id,
			PageSize: 100,
		})
		wsReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", exchange.LocoToken))

		wsResp, err := wsClient.ListOrgWorkspaces(ctx, wsReq)
		if err != nil {
			slog.Debug("failed to get workspaces for org", "orgId", selectedOrg.Id, "error", err)
			return fmt.Errorf("failed to list workspaces: %w", err)
		}

		workspaces := wsResp.Msg.Workspaces
		if len(workspaces) == 0 {
			return fmt.Errorf("organization has no workspaces")
		}

		selectedWorkspace = workspaces[0]
	}

	cfg := session.NewSessionConfig()
	if err := cfg.SetDefaultScope(
		session.SimpleOrg{ID: selectedOrg.Id, Name: selectedOrg.Name},
		session.SimpleWorkspace{ID: selectedWorkspace.Id, Name: selectedWorkspace.Name},
	); err != nil {
		slog.Error(err.Error())
		return err
	}

	if storeErr := store.Set(newToken); storeErr != nil {
		return fmt.Errorf("failed to store token: %w", storeErr)
	}

	checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
	title := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Authentication successful!")
	orgLine := lipgloss.NewStyle().
		Foreground(ui.LocoLightGray).
		Render(fmt.Sprintf("  Organization: %s", selectedOrg.Name))
	wsLine := lipgloss.NewStyle().
		Foreground(ui.LocoLightGray).
		Render(fmt.Sprintf("  Workspace: %s", selectedWorkspace.Name))
	fmt.Printf("%s %s\n%s\n%s\n", checkmark, title, orgLine, wsLine)

	return nil
}

func tokenFromExchange(resp *oAuth.ExchangeOAuthTokenResponse) keychain.UserToken {
	lifetime := time.Duration(resp.GetExpiresIn())*time.Second - 10*time.Minute
	expiresAt := time.Now().Add(lifetime)
	return keychain.UserToken{
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

		resp, err := c.Post("/login/oauth/access_token", authTokenRequest, headers)
		if err != nil {
			apiError, ok := errors.AsType[*api.APIError](err)
			if !ok {
				slog.Debug("network error while polling for token", "error", err)
				return nil, fmt.Errorf("network error: %w", err)
			}
			switch apiError.StatusCode {
			case 400:
				slog.Debug("authorization pending", "status_code", apiError.StatusCode)
				continue
			case 403:
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
			errorStyle := lipgloss.NewStyle().Foreground(ui.LocoRed).Bold(true)
			return tea.NewView(fmt.Sprintf("%s\n%s\n",
				errorStyle.Render("Authentication failed:"),
				lipgloss.NewStyle().Foreground(ui.LocoDarkGray).Render(m.err.Error())))
		}
		return tea.NewView(
			lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render("Setting up organization and workspace...") + "\n",
		)
	}

	codeStyle := lipgloss.NewStyle().Foreground(ui.LocoOrange).Bold(true).Padding(0, 0)
	urlStyle := lipgloss.NewStyle().Foreground(ui.LocoOrange).Underline(true)
	instructionStyle := lipgloss.NewStyle().Foreground(ui.LocoLightGray)
	spinnerStyle := lipgloss.NewStyle().Foreground(ui.LocoOrange).Bold(true)

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
		lipgloss.NewStyle().Foreground(ui.LocoLightGray).Faint(true).Render("Press 'q' or Ctrl+C to quit"),
	))
}
