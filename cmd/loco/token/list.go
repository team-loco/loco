package token

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/token/internal"
	tokenv1 "github.com/team-loco/loco/shared/proto/token/v1"
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
)

type listRunner struct {
	deps *internal.TokenDeps
}

func buildListCmd(deps *internal.TokenDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		Long:  "List all personal access tokens for your account.",
		Args:  cobra.NoArgs,
	}

	if deps != nil {
		runner := &listRunner{deps: deps}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *listRunner) Run(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Get current user
	whoAmIReq := connect.NewRequest(&userv1.WhoAmIRequest{})
	whoAmIReq.Header().Set("Authorization", r.deps.AuthHeader())

	whoAmIResp, err := r.deps.WhoAmI(ctx, whoAmIReq)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	req := connect.NewRequest(&tokenv1.ListTokensRequest{
		EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
		EntityId:   whoAmIResp.Msg.User.Id,
	})
	req.Header().Set("Authorization", r.deps.AuthHeader())

	resp, err := r.deps.ListTokens(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list tokens: %w", err)
	}

	if len(resp.Msg.Tokens) == 0 {
		fmt.Fprintln(r.deps.Stdout, "No tokens found.")
		return nil
	}

	fmt.Fprintln(r.deps.Stdout, "Tokens:")
	for _, t := range resp.Msg.Tokens {
		expiresAt := "never"
		if t.ExpiresAt != nil {
			exp := t.ExpiresAt.AsTime()
			if exp.Before(time.Now()) {
				expiresAt = fmt.Sprintf("%s (expired)", exp.Format("2006-01-02"))
			} else {
				expiresAt = exp.Format("2006-01-02")
			}
		}

		scopes := formatScopes(t.Scopes)
		fmt.Fprintf(r.deps.Stdout, "  - %s (scopes: %s, expires: %s)\n", t.Name, scopes, expiresAt)
	}

	return nil
}

func formatScopes(scopes []*tokenv1.EntityScope) string {
	if len(scopes) == 0 {
		return "none"
	}

	var scopeNames []string
	for _, s := range scopes {
		switch s.Scope {
		case tokenv1.Scope_SCOPE_READ:
			scopeNames = append(scopeNames, "read")
		case tokenv1.Scope_SCOPE_WRITE:
			scopeNames = append(scopeNames, "write")
		case tokenv1.Scope_SCOPE_ADMIN:
			scopeNames = append(scopeNames, "admin")
		}
	}

	if len(scopeNames) == 0 {
		return "none"
	}

	result := scopeNames[0]
	for i := 1; i < len(scopeNames); i++ {
		result += ", " + scopeNames[i]
	}
	return result
}
