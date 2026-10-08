package loco

import (
	"context"
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
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

func newLoginCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Loco",
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			httpClient := httputil.NewHTTPClient()
			config, err := configv1connect.NewConfigServiceClient(httpClient, host).
				GetConfig(cmd.Context(), connect.NewRequest(&configv1.GetConfigRequest{}))
			if err != nil {
				cmdutil.LogRequestID(cmd.Context(), err, "failed to read server config")
				return fmt.Errorf("login to %s failed: %w", host, err)
			}
			if config.Msg.GetAuth() == nil {
				return fmt.Errorf("login to %s failed: %w", host, errNoIdentityProvider)
			}
			return providerLogin(cmd, httpClient, host, store)
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	cmd.Flags().Bool("device", false, "Sign in with a code from another device, for machines without a browser")
	cmd.Flags().String("web-host", "", "Web UI URL that approves the sign-in")
	return cmd
}

func providerLogin(cmd *cobra.Command, httpClient *http.Client, host string, store keychain.TokenStore) error {
	ctx := cmd.Context()
	device, err := cmd.Flags().GetBool("device")
	if err != nil {
		return err
	}
	authClient := authv1connect.NewAuthServiceClient(httpClient, host)
	var tokens *authv1.CLITokens
	if device {
		tokens, err = deviceLogin(ctx, authClient)
	} else {
		webHost, webErr := cmdutil.GetWebHost(cmd)
		if webErr != nil {
			return webErr
		}
		tokens, err = browserLogin(ctx, authClient, webHost, openBrowser)
	}
	if err != nil {
		return fmt.Errorf("login to %s failed: %w", host, err)
	}
	if err := setupLoginScope(ctx, httpClient, host, store, *cmdutil.TokenFromCLITokens(host, tokens)); err != nil {
		return fmt.Errorf("login to %s failed: %w", host, err)
	}
	return nil
}

func setupLoginScope(
	ctx context.Context,
	httpClient *http.Client,
	host string,
	store keychain.TokenStore,
	newToken keychain.UserToken,
) error {
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

	org, workspace, err := resolveLoginScope(ctx, httpClient, host, newToken.Token)
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

type (
	tickMsg        time.Time
	authSuccessMsg struct {
		Tokens *authv1.CLITokens
	}
	authErrorMsg struct {
		Error error
	}
)

func waitForToken(tokenChan <-chan *authv1.CLITokens) tea.Cmd {
	return func() tea.Msg {
		return authSuccessMsg{Tokens: <-tokenChan}
	}
}

func waitForError(errorChan <-chan error) tea.Cmd {
	return func() tea.Msg {
		err := <-errorChan
		return authErrorMsg{Error: err}
	}
}

type model struct {
	tokens          *authv1.CLITokens
	tokenChan       <-chan *authv1.CLITokens
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
	tokenChan <-chan *authv1.CLITokens,
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
		m.tokens = msg.Tokens
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
