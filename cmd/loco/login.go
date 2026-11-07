package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/internal/host"

	"github.com/team-loco/loco/internal/rpc"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/internal/session"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/httputil"
)

const spinnerFrameDelay = 100 * time.Millisecond

func newLoginCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Loco",
		RunE: func(cmd *cobra.Command, _ []string) error {
			apiHost, err := host.GetHost(cmd)
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

			if err == nil && t.Host != apiHost {
				slog.Debug("stored token belongs to another host", "token_host", t.Host, "host", apiHost)
			}
			if err == nil && t.Host == apiHost {
				if !t.ExpiresAt.Before(time.Now().Add(1 * time.Hour)) {
					checkmark := lipgloss.NewStyle().Foreground(ui.Ok).Render("✔")
					message := lipgloss.NewStyle().Bold(true).Foreground(ui.Accent).Render("Already logged in!")
					subtext := lipgloss.NewStyle().
						Foreground(ui.Fg2).
						Render("You can continue using loco")

					_, err = lipgloss.Printf("%s %s\n%s\n", checkmark, message, subtext)
					return err
				}
				slog.Debug("token is expired or will expire soon", "expires_at", t.ExpiresAt)
			} else {
				slog.Debug("no token found in keychain", "error", err)
			}
			httpClient := httputil.NewHTTPClient()
			configClients := rpc.NewHTTPClients(httpClient, apiHost, "")
			configClient := configClients.Config
			config, err := configClient.GetConfig(cmd.Context(), &configv1.GetConfigRequest{})
			if err != nil {
				return fmt.Errorf("login to %s failed: %w", apiHost, err)
			}
			if config.GetAuth() == nil {
				return fmt.Errorf("login to %s failed: %w", apiHost, errNoIdentityProvider)
			}
			return providerLogin(cmd, httpClient, apiHost, store)
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	cmd.Flags().Bool("device", false, "Sign in with a code from another device, for machines without a browser")
	cmd.Flags().String("web-host", "", "Web UI URL that approves the sign-in")
	return cmd
}

func providerLogin(cmd *cobra.Command, httpClient *http.Client, apiHost string, store keychain.TokenStore) error {
	ctx := cmd.Context()
	device, err := cmd.Flags().GetBool("device")
	if err != nil {
		return err
	}
	interactive := ui.IsInteractive()
	if !device && !interactive {
		return fmt.Errorf("login to %s failed: %w", apiHost, errBrowserLoginNeedsTerminal)
	}
	authClients := rpc.NewHTTPClients(httpClient, apiHost, "")
	authClient := authClients.Auth
	var tokens *authv1.CLITokens
	if device {
		tokens, err = deviceLogin(ctx, authClient, interactive)
	} else {
		webHost, webErr := host.GetWebHost(cmd)
		if webErr != nil {
			return webErr
		}
		tokens, err = browserLogin(ctx, authClient, webHost, openBrowser)
	}
	if err != nil {
		return fmt.Errorf("login to %s failed: %w", apiHost, err)
	}
	if err := setupLoginScope(ctx, httpClient, apiHost, store, *auth.TokenFromCLITokens(apiHost, tokens)); err != nil {
		return fmt.Errorf("login to %s failed: %w", apiHost, err)
	}
	return nil
}

func setupLoginScope(
	ctx context.Context,
	httpClient *http.Client,
	apiHost string,
	store keychain.TokenStore,
	newToken keychain.UserToken,
) error {
	cfg, err := session.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.LocoHost == apiHost {
		scope, scopeErr := cfg.GetScope()
		if scopeErr == nil {
			if storeErr := store.Set(newToken); storeErr != nil {
				return fmt.Errorf("failed to store token: %w", storeErr)
			}
			return printLoginSuccess("Logged in!", scope.Organization.Name, scope.Workspace.Name)
		}
	}

	org, workspace, err := resolveLoginScope(ctx, httpClient, apiHost, newToken.Token)
	if err != nil {
		return err
	}

	cfg.LocoHost = apiHost
	cfg.Scopes = make(map[string]*session.Scope)
	if err := cfg.SetDefaultScope(org, workspace); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if storeErr := store.Set(newToken); storeErr != nil {
		return fmt.Errorf("failed to store token: %w", storeErr)
	}

	return printLoginSuccess("Authentication successful!", org.Name, workspace.Name)
}

const loginPageSize = 100

func resolveLoginScope(
	ctx context.Context,
	httpClient *http.Client,
	apiHost string,
	locoToken string,
) (session.SimpleOrg, session.SimpleWorkspace, error) {
	clients := rpc.NewHTTPClients(httpClient, apiHost, locoToken)
	orgClient := clients.Org
	wsClient := clients.Workspace
	userClient := clients.User

	currentUserReq := &userv1.WhoAmIRequest{}

	currentUserResp, err := userClient.WhoAmI(ctx, currentUserReq)
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to get current user: %w", err)
	}

	orgs, err := cmdutil.Paginate(func(token string) ([]*orgv1.Organization, string, error) {
		resp, listErr := orgClient.ListUserOrgs(ctx, &orgv1.ListUserOrgsRequest{
			UserId:          currentUserResp.GetUser().GetId(),
			PageSize:        loginPageSize,
			PageToken:       token,
			CreateIfMissing: true,
		})
		if listErr != nil {
			return nil, "", listErr
		}
		return resp.GetOrgs(), resp.GetNextPageToken(), nil
	})
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to list organizations: %w", err)
	}

	if len(orgs) == 0 {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, errNoOrganization
	}
	selectedOrg := orgs[0]
	workspaces, err := cmdutil.Paginate(func(token string) ([]*workspacev1.Workspace, string, error) {
		resp, listErr := wsClient.ListOrgWorkspaces(ctx, &workspacev1.ListOrgWorkspacesRequest{
			OrgId:     selectedOrg.GetId(),
			PageSize:  loginPageSize,
			PageToken: token,
		})
		if listErr != nil {
			return nil, "", listErr
		}
		return resp.GetWorkspaces(), resp.GetNextPageToken(), nil
	})
	if err != nil {
		return session.SimpleOrg{}, session.SimpleWorkspace{}, fmt.Errorf("failed to list workspaces: %w", err)
	}
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

func printLoginSuccess(title, orgName, workspaceName string) error {
	checkmark := lipgloss.NewStyle().Foreground(ui.Ok).Render("✔")
	heading := lipgloss.NewStyle().Bold(true).Foreground(ui.Accent).Render(title)
	orgLine := lipgloss.NewStyle().
		Foreground(ui.Fg2).
		Render(fmt.Sprintf("  Organization: %s", orgName))
	wsLine := lipgloss.NewStyle().
		Foreground(ui.Fg2).
		Render(fmt.Sprintf("  Workspace: %s", workspaceName))
	_, err := lipgloss.Printf("%s %s\n%s\n%s\n", checkmark, heading, orgLine, wsLine)
	return err
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

type model struct {
	tokens          *authv1.CLITokens
	poll            tea.Cmd
	userCode        string
	verificationURI string
	err             error
	frameIndex      int
	polling         bool
	done            bool
}

var loadingFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func initialModel(userCode, verificationURI string, poll tea.Cmd) model {
	return model{
		userCode:        userCode,
		verificationURI: verificationURI,
		polling:         true,
		poll:            poll,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(), m.poll)
}

func tick() tea.Cmd {
	return tea.Tick(spinnerFrameDelay, func(t time.Time) tea.Msg {
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
			m.frameIndex = (m.frameIndex + 1) % len(loadingFrames)
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

	return tea.NewView(fmt.Sprintf(
		"%s %s\n\n%s %s\n\n%s %s\n\n%s",
		instructionStyle.Render("Please open the following URL in your browser:"),
		urlStyle.Render(m.verificationURI),
		instructionStyle.Render("Then, enter the following user code:"),
		codeStyle.Render(m.userCode),
		spinnerStyle.Render(loadingFrames[m.frameIndex]),
		instructionStyle.Render("Waiting for authentication..."),
		lipgloss.NewStyle().Foreground(ui.Fg3).Faint(true).Render("Press 'q' or Ctrl+C to quit"),
	))
}
