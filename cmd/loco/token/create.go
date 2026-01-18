package token

import (
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/token/internal"
	tokenv1 "github.com/team-loco/loco/shared/proto/loco/token/v1"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
)

type createRunner struct {
	deps *internal.TokenDeps
	name string
}

func buildCreateCmd(deps *internal.TokenDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new API token",
		Long: `Create a new personal access token with specified scopes.

The token string is only displayed once upon creation - save it securely.

Examples:
  loco token create my-ci-token --scope read
  loco token create deploy-token --scope write --expires 7d`,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringSlice("scope", []string{"read"}, "Token scopes: read, write, admin")
	cmd.Flags().String("expires", "30d", "Token expiration (e.g., 1d, 7d, 30d)")

	if deps != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			runner := &createRunner{deps: deps, name: args[0]}
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *createRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Get current user
	whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	whoAmIReq.Header().Set("Authorization", r.deps.AuthHeader())

	whoAmIResp, err := r.deps.WhoAmI(ctx, whoAmIReq)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	// Parse scopes
	scopeStrs, _ := cmd.Flags().GetStringSlice("scope")
	var scopes []*tokenv1.EntityScope
	for _, s := range scopeStrs {
		scope, err := parseScope(s)
		if err != nil {
			return err
		}
		scopes = append(scopes, &tokenv1.EntityScope{
			Scope:      scope,
			EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
			EntityId:   whoAmIResp.Msg.User.Id,
		})
	}

	// Parse expiration
	expiresStr, _ := cmd.Flags().GetString("expires")
	expiresSec, err := parseDuration(expiresStr)
	if err != nil {
		return err
	}

	req := connect.NewRequest(&tokenv1.CreateTokenRequest{
		Name:         r.name,
		EntityType:   tokenv1.EntityType_ENTITY_TYPE_USER,
		EntityId:     whoAmIResp.Msg.User.Id,
		Scopes:       scopes,
		ExpiresInSec: expiresSec,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.CreateToken(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}

	fmt.Fprintln(r.deps.Stdout, "Token created successfully!")
	fmt.Fprintln(r.deps.Stdout, "")
	fmt.Fprintf(r.deps.Stdout, "Name: %s\n", resp.Msg.TokenMetadata.Name)
	fmt.Fprintf(r.deps.Stdout, "Token: %s\n", resp.Msg.Token)
	fmt.Fprintln(r.deps.Stdout, "")
	fmt.Fprintln(r.deps.Stdout, "⚠️  Save this token - it won't be shown again!")

	return nil
}

func parseScope(s string) (tokenv1.Scope, error) {
	switch strings.ToLower(s) {
	case "read":
		return tokenv1.Scope_SCOPE_READ, nil
	case "write":
		return tokenv1.Scope_SCOPE_WRITE, nil
	case "admin":
		return tokenv1.Scope_SCOPE_ADMIN, nil
	default:
		return tokenv1.Scope_SCOPE_UNSPECIFIED, fmt.Errorf("invalid scope %q: must be read, write, or admin", s)
	}
}

func parseDuration(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return int64(d) * 24 * 60 * 60, nil
	}

	// Try parsing as Go duration
	dur, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: use format like 1d, 7d, 30d or Go duration", s)
	}
	return int64(dur.Seconds()), nil
}
