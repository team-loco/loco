package loco

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	osUser "os/user"
	"strings"
	"time"

	"connectrpc.com/connect"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/api"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	oAuth "github.com/team-loco/loco/shared/proto/oauth/v1"
	"github.com/team-loco/loco/shared/proto/oauth/v1/oauthv1connect"
	orgv1 "github.com/team-loco/loco/shared/proto/org/v1"
	"github.com/team-loco/loco/shared/proto/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
	"github.com/team-loco/loco/shared/proto/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/shared/proto/workspace/v1"
	"github.com/team-loco/loco/shared/proto/workspace/v1/workspacev1connect"
)

type DeviceCodeRequest struct {
	ClientId string `json:"client_id"`
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
	ClientId   string `json:"client_id"`
	DeviceCode string `json:"device_code"`
	GrantType  string `json:"grant_type"`
}

type AuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

type TokenDetails struct {
	ClientId string  `json:"clientId"`
	TokenTTL float64 `json:"tokenTTL"`
}

func init() {
	loginCmd.Flags().String("host", "", "Set the host URL")
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to loco via Github OAuth",
	RunE: func(cmd *cobra.Command, args []string) error {
		host, err := getHost(cmd)
		if err != nil {
			return err
		}
		user, err := osUser.Current()
		if err != nil {
			slog.Debug("failed to get current user", "error", err)
			return err
		}

		t, err := keychain.GetLocoToken(user.Name)
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

		httpClient := shared.NewHTTPClient()
		oAuthClient := oauthv1connect.NewOAuthServiceClient(httpClient, host)
		resp, err := oAuthClient.GithubOAuthDetails(cmd.Context(), connect.NewRequest(&oAuth.GithubOAuthDetailsRequest{}))
		if err != nil {
			logRequestID(cmd.Context(), err, "failed to get oAuth details")
			return err
		}
		slog.Debug("retrieved oauth details", "client_id", resp.Msg.ClientId)

		payload := DeviceCodeRequest{
			ClientId: resp.Msg.ClientId,
			Scope:    "read:user user:email",
		}

		req, err := c.Post("/login/device/code", payload, map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
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

		go func() {
			pollErr := pollAuthToken(c, payload.ClientId, deviceTokenResponse.DeviceCode, deviceTokenResponse.Interval, tokenChan)
			if pollErr != nil {
				fmt.Println(pollErr.Error())
				errorChan <- pollErr
			}
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
			slog.Debug("received auth token from github oauth", "token", finalM.tokenResp.AccessToken)
		}

		if finalM.tokenResp == nil {
			return nil
		}

		locoResp, err := oAuthClient.ExchangeGithubToken(cmd.Context(), connect.NewRequest(&oAuth.ExchangeGithubTokenRequest{
			GithubAccessToken:     finalM.tokenResp.AccessToken,
			CreateUserIfNotExists: true,
		}))
		if err != nil {
			return err
		}

		orgClient := orgv1connect.NewOrgServiceClient(httpClient, host)
		wsClient := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)

		existingCfg, err := config.Load()
		if err != nil {
			slog.Debug("failed to load existing config", "error", err)
		}

		// use existing scope if it exists.
		if existingCfg != nil {
			scope, err := existingCfg.GetScope()
			if err == nil {
				keychain.SetLocoToken(user.Name, keychain.UserToken{
					Token: locoResp.Msg.LocoToken,
					// sub 10 mins
					ExpiresAt: time.Now().Add(time.Duration(locoResp.Msg.ExpiresIn)*time.Second - (10 * time.Minute)),
				})

				checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
				title := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Logged in!")
				orgLine := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render(fmt.Sprintf("  Organization: %s", scope.Organization.Name))
				wsLine := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render(fmt.Sprintf("  Workspace: %s", scope.Workspace.Name))
				fmt.Printf("%s %s\n%s\n%s\n", checkmark, title, orgLine, wsLine)
				return nil
			}
		}

		var selectedOrg *orgv1.Organization
		var selectedWorkspace *workspacev1.Workspace

		orgRequest := connect.NewRequest(&orgv1.GetCurrentUserOrgsRequest{})
		orgRequest.Header().Add("Authorization", fmt.Sprintf("Bearer %s", locoResp.Msg.LocoToken))

		orgResp, err := orgClient.GetCurrentUserOrgs(context.Background(), orgRequest)
		if err != nil {
			slog.Debug("failed to get user orgs details", "error", err)
			return err
		}

		userClient := userv1connect.NewUserServiceClient(httpClient, host)

		currentUserReq := connect.NewRequest(&userv1.GetCurrentUserRequest{})
		currentUserReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", locoResp.Msg.LocoToken))

		currentUserResp, err := userClient.GetCurrentUser(context.Background(), currentUserReq)
		if err != nil {
			slog.Debug("failed to create current user", "error", err)
			return fmt.Errorf("failed to get current user: %w", err)
		}

		email := currentUserResp.Msg.GetUser().GetEmail()
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
			createOrgReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", locoResp.Msg.LocoToken))

			createOrgResp, err := orgClient.CreateOrg(context.Background(), createOrgReq)
			if err != nil {
				slog.Debug("failed to create organization", "error", err)
				return fmt.Errorf("failed to create organization: %w", err)
			}

			createdOrg := createOrgResp.Msg.GetOrg()
			if createdOrg == nil {
				return fmt.Errorf("organization creation returned empty response")
			}

			workspaceName := "default"
			wsClient := workspacev1connect.NewWorkspaceServiceClient(httpClient, host)
			createWSReq := connect.NewRequest(&workspacev1.CreateWorkspaceRequest{
				OrgId: createdOrg.Id,
				Name:  workspaceName,
			})
			createWSReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", locoResp.Msg.LocoToken))

			createWSResp, err := wsClient.CreateWorkspace(context.Background(), createWSReq)
			if err != nil {
				slog.Debug("failed to create workspace", "error", err)
				return fmt.Errorf("failed to create workspace: %w", err)
			}

			createdWS := createWSResp.Msg.GetWorkspace()
			if createdWS == nil {
				return fmt.Errorf("workspace creation returned empty response")
			}

			cfg := config.NewSessionConfig()
			if err := cfg.SetDefaultScope(
				config.SimpleOrg{ID: createdOrg.Id, Name: createdOrg.Name},
				config.SimpleWorkspace{ID: createdWS.Id, Name: createdWS.Name},
			); err != nil {
				slog.Error(err.Error())
				return err
			}

			keychain.SetLocoToken(user.Name, keychain.UserToken{
				Token: locoResp.Msg.LocoToken,
				// sub 10 mins
				ExpiresAt: time.Now().Add(time.Duration(locoResp.Msg.ExpiresIn)*time.Second - (10 * time.Minute)),
			})

			checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
			title := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Authentication successful!")
			orgLine := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render(fmt.Sprintf("  Organization: %s", createdOrg.Name))
			wsLine := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render(fmt.Sprintf("  Workspace: %s", createdWS.Name))
			fmt.Printf("%s %s\n%s\n%s\n", checkmark, title, orgLine, wsLine)

			return nil
		}

		if len(orgs) == 1 {
			selectedOrg = orgs[0]

			wsReq := connect.NewRequest(&workspacev1.ListWorkspacesRequest{
				OrgId: selectedOrg.Id,
			})
			wsReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", locoResp.Msg.LocoToken))

			wsResp, err := wsClient.ListWorkspaces(context.Background(), wsReq)
			if err != nil {
				slog.Debug("failed to get workspaces for org", "orgId", selectedOrg.Id, "error", err)
				return fmt.Errorf("failed to list workspaces: %w", err)
			}

			workspaces := wsResp.Msg.GetWorkspaces()
			if len(workspaces) == 0 {
				return fmt.Errorf("organization has no workspaces")
			}

			selectedWorkspace = workspaces[0]
		} else {
			selectedOrg = orgs[0]

			wsReq := connect.NewRequest(&workspacev1.ListWorkspacesRequest{
				OrgId: selectedOrg.Id,
			})
			wsReq.Header().Add("Authorization", fmt.Sprintf("Bearer %s", locoResp.Msg.LocoToken))

			wsResp, err := wsClient.ListWorkspaces(context.Background(), wsReq)
			if err != nil {
				slog.Debug("failed to get workspaces for org", "orgId", selectedOrg.Id, "error", err)
				return fmt.Errorf("failed to list workspaces: %w", err)
			}

			workspaces := wsResp.Msg.GetWorkspaces()
			if len(workspaces) == 0 {
				return fmt.Errorf("organization has no workspaces")
			}

			selectedWorkspace = workspaces[0]
		}

		cfg := config.NewSessionConfig()
		if err := cfg.SetDefaultScope(
			config.SimpleOrg{ID: selectedOrg.Id, Name: selectedOrg.Name},
			config.SimpleWorkspace{ID: selectedWorkspace.Id, Name: selectedWorkspace.Name},
		); err != nil {
			slog.Error(err.Error())
			return err
		}

		keychain.SetLocoToken(user.Name, keychain.UserToken{
			Token: locoResp.Msg.LocoToken,
			// sub 10 mins
			ExpiresAt: time.Now().Add(time.Duration(locoResp.Msg.ExpiresIn)*time.Second - (10 * time.Minute)),
		})

		checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
		title := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Authentication successful!")
		orgLine := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render(fmt.Sprintf("  Organization: %s", selectedOrg.Name))
		wsLine := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render(fmt.Sprintf("  Workspace: %s", selectedWorkspace.Name))
		fmt.Printf("%s %s\n%s\n%s\n", checkmark, title, orgLine, wsLine)

		return nil
	},
}

func pollAuthToken(c *api.Client, clientId string, deviceCode string, interval int, tokenChan chan AuthTokenResponse) error {
	authTokenRequest := AuthTokenRequest{
		ClientId:   clientId,
		DeviceCode: deviceCode,
		GrantType:  "urn:ietf:params:oauth:grant-type:device_code",
	}

	for {
		resp, err := c.Post("/login/oauth/access_token", authTokenRequest, map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		})
		if err != nil {
			if apiError, ok := err.(*api.APIError); ok {
				switch apiError.StatusCode {
				case 400:
					slog.Debug("authorization pending", "status_code", apiError.StatusCode)
					time.Sleep(time.Duration(interval) * time.Second)
					continue
				case 403: // rate limit or access denied
					slog.Debug("access denied or rate limited", "status_code", apiError.StatusCode, "error", err)
					return fmt.Errorf("access denied or rate limited: %w", err)
				default:
					slog.Debug("API error while polling for token", "status_code", apiError.StatusCode, "error", err)
					return fmt.Errorf("API error: %w", err)
				}
			} else {
				slog.Debug("network error while polling for token", "error", err)
				return fmt.Errorf("network error: %w", err)
			}
		}

		authTokenResponse := new(AuthTokenResponse)
		err = json.Unmarshal(resp, authTokenResponse)
		if err != nil {
			slog.Debug("failed to unmarshal auth token response", "error", err)
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}

		if authTokenResponse.AccessToken != "" {
			tokenChan <- *authTokenResponse
			break
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}

	return nil
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
	loadingFrames   []string
	userCode        string
	verificationURI string
	err             error
	tokenChan       <-chan AuthTokenResponse
	errorChan       <-chan error
	tokenResp       *AuthTokenResponse
	frameIndex      int
	polling         bool
	done            bool
}

func initialModel(userCode string, verificationUri string, tokenChan <-chan AuthTokenResponse, errorChan <-chan error) model {
	return model{
		userCode:        userCode,
		verificationURI: verificationUri,
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
	case tea.KeyMsg:
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

func (m model) View() string {
	if m.done {
		if m.err != nil {
			errorStyle := lipgloss.NewStyle().Foreground(ui.LocoRed).Bold(true)
			return fmt.Sprintf("%s\n%s\n",
				errorStyle.Render("Authentication failed:"),
				lipgloss.NewStyle().Foreground(ui.LocoDarkGray).Render(m.err.Error()))
		}
		return lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render("Setting up organization and workspace...") + "\n"
	}

	codeStyle := lipgloss.NewStyle().Foreground(ui.LocoOrange).Bold(true).Padding(0, 0)
	urlStyle := lipgloss.NewStyle().Foreground(ui.LocoOrange).Underline(true)
	instructionStyle := lipgloss.NewStyle().Foreground(ui.LocoLightGray)
	spinnerStyle := lipgloss.NewStyle().Foreground(ui.LocoOrange).Bold(true)

	spinner := ""
	if len(m.loadingFrames) > 0 {
		spinner = spinnerStyle.Render(m.loadingFrames[m.frameIndex])
	}

	return fmt.Sprintf(
		"%s %s\n\n%s %s\n\n%s %s\n\n%s",
		instructionStyle.Render("Please open the following URL in your browser:"),
		urlStyle.Render(m.verificationURI),
		instructionStyle.Render("Then, enter the following user code:"),
		codeStyle.Render(m.userCode),
		spinner,
		instructionStyle.Render("Waiting for authentication..."),
		lipgloss.NewStyle().Foreground(ui.LocoLightGray).Faint(true).Render("Press 'q' or Ctrl+C to quit"),
	)
}
