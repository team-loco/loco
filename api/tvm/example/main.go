package main

import (
	"context"
	"fmt"
	"time"

	"github.com/loco-team/loco/api/db"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/tvm"
)

func main() {
	ctx := context.Background()
	db, err := db.NewDB(ctx, "postgres://loco_user:@localhost:5432/loco?sslmode=disable")
	queries := genDb.New(db.Pool())
	handleErr(err)

	machine := tvm.NewVendingMachine(queries, tvm.Config{
		MaxTokenDuration:   time.Hour * 24,
		LoginTokenDuration: time.Hour * 1,
	})

	token, err := machine.ExchangeGithub(ctx, "[githeb]")
	handleErr(err)

	fmt.Println("exchanged token:", token)

	// user:read on self
	ok := machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeUser,
				ID:   1,
			},
			Scope: genDb.ScopeRead,
		},
	})
	fmt.Println("user:read on self", checkmark(ok == nil))

	// user:write on self
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeUser,
				ID:   1,
			},
			Scope: genDb.ScopeWrite,
		},
	})
	fmt.Println("user:write on self", checkmark(ok == nil))

	// user:admin on self
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeUser,
				ID:   1,
			},
			Scope: genDb.ScopeAdmin,
		},
	})
	fmt.Println("user:admin on self", checkmark(ok == nil))

	// user:read on other
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeUser,
				ID:   2,
			},
			Scope: genDb.ScopeRead,
		},
	})
	fmt.Println("user:read on other", checkmark(ok != nil))

	// user:read on org 1 (fail bc user does not have org:read)
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeOrganization,
				ID:   1,
			},
			Scope: genDb.ScopeRead,
		},
	})
	fmt.Println("org 1 read", checkmark(ok != nil))

	// user:write on org 1 (pass bc user has org:write)
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeOrganization,
				ID:   1,
			},
			Scope: genDb.ScopeWrite,
		},
	})
	fmt.Println("org 1 write", checkmark(ok == nil))

	// user:admin on org 1 (fail bc user does not have org:admin)
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeOrganization,
				ID:   1,
			},
			Scope: genDb.ScopeAdmin,
		},
	})
	fmt.Println("org 1 admin", checkmark(ok != nil))

	// user:read on org 2 (fail bc user has does not have org:read on org 2)
	ok = machine.VerifyAccess(ctx, token, []genDb.EntityScope{
		{
			Entity: genDb.Entity{
				Type: genDb.EntityTypeOrganization,
				ID:   2,
			},
			Scope: genDb.ScopeRead,
		},
	})
	fmt.Println("org 2 read", checkmark(ok != nil))

	// now lets test actions

	machine.VerifyAccess(context.Background(), token, tvm.Action(genDb.Entity{
		Type: genDb.EntityTypeOrganization,
		ID:   1,
	}, tvm.ActionListWorkspaces)) // should fail because user does not have org:read on org 1
	fmt.Println("action list workspaces on org 1", checkmark(ok != nil))

	ok = machine.VerifyAccess(context.Background(), token, tvm.Action(genDb.Entity{
		Type: genDb.EntityTypeOrganization,
		ID:   1,
	}, tvm.ActionCreateWorkspace)) // should pass because user has org:write on org 1
	fmt.Println("action create workspace on org 1", checkmark(ok == nil))

	ok = machine.VerifyAccess(context.Background(), token, tvm.Action(genDb.Entity{
		Type: genDb.EntityTypeOrganization,
		ID:   1,
	}, tvm.ActionDeleteOrg)) // should fail because user does not have org:admin on org 1
	fmt.Println("action delete org on org 1", checkmark(ok != nil))

	ok = machine.VerifyAccess(context.Background(), token, tvm.Action(genDb.Entity{
		Type: genDb.EntityTypeWorkspace,
		ID:   1,
	}, tvm.ActionDeleteWorkspace)) // should pass because user had org:write on org 1
	fmt.Println("action delete workspace on org 1", checkmark(ok == nil))

	ok = machine.VerifyAccess(context.Background(), token, tvm.Action(genDb.Entity{
		Type: genDb.EntityTypeUser,
		ID:   1,
	}, tvm.ActionEditUserInfo)) // should pass because user has user:write on self
	fmt.Println("action edit user info on self", checkmark(ok == nil))
}

func checkmark(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
