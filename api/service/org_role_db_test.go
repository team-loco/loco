package service

import (
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestSetOrgMemberRole(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	outsider := o.api.signUp(t, "outsider")
	o.addOrgMember(t, member)
	o.grantWorkspace(t, member, o.shared)
	ctx := t.Context()
	set := func(caller membershipUser, target string, role orgv1.OrgRole) error {
		t.Helper()
		_, err := caller.clients.Org.SetOrgMemberRole(
			ctx,
			&orgv1.SetOrgMemberRoleRequest{OrgId: o.orgID, UserId: target, Role: role},
		)
		return err
	}
	for _, caller := range []membershipUser{member, outsider} {
		if err := set(
			caller,
			member.id.String(),
			orgv1.OrgRole_ORG_ROLE_ADMIN,
		); codeOf(
			err,
		) != connect.CodePermissionDenied {
			t.Fatalf("non-admin: %v", err)
		}
	}
	for _, target := range []string{outsider.id.String(), uuid.NewString()} {
		if err := set(o.owner, target, orgv1.OrgRole_ORG_ROLE_ADMIN); codeOf(err) != connect.CodeNotFound {
			t.Fatalf("non-member: %v", err)
		}
	}
	for _, role := range []orgv1.OrgRole{orgv1.OrgRole_ORG_ROLE_UNSPECIFIED, orgv1.OrgRole(99)} {
		if err := set(o.owner, member.id.String(), role); codeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid role: %v", err)
		}
	}
	if err := set(
		o.owner,
		o.owner.id.String(),
		orgv1.OrgRole_ORG_ROLE_MEMBER,
	); codeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("last admin self-demotion: %v", err)
	}
	if err := set(o.owner, member.id.String(), orgv1.OrgRole_ORG_ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	if _, err := member.clients.Workspace.GetWorkspace(
		ctx,
		&workspacev1.GetWorkspaceRequest{WorkspaceId: o.sibling},
	); err != nil {
		t.Fatalf("promoted admin: %v", err)
	}
	if err := set(member, o.owner.id.String(), orgv1.OrgRole_ORG_ROLE_MEMBER); err != nil {
		t.Fatalf("demote another admin: %v", err)
	}
	if err := set(
		o.owner,
		member.id.String(),
		orgv1.OrgRole_ORG_ROLE_MEMBER,
	); codeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("demoted caller: %v", err)
	}
	if err := set(
		member,
		member.id.String(),
		orgv1.OrgRole_ORG_ROLE_MEMBER,
	); codeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("last admin: %v", err)
	}
	if err := set(member, o.owner.id.String(), orgv1.OrgRole_ORG_ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	if err := set(member, member.id.String(), orgv1.OrgRole_ORG_ROLE_MEMBER); err != nil {
		t.Fatalf("self-demote with another admin: %v", err)
	}
	scopes, err := o.api.queries.GetUserScopesOnEntity(
		ctx,
		genDb.GetUserScopesOnEntityParams{
			UserID:     member.id,
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   uuid.MustParse(o.orgID),
		},
	)
	if err != nil || len(scopes) != 1 || scopes[0].Scope != genDb.ScopeMember {
		t.Fatalf("demoted scopes = %v: %v", scopes, err)
	}
	if _, err = member.clients.Workspace.GetWorkspace(
		ctx,
		&workspacev1.GetWorkspaceRequest{WorkspaceId: o.shared},
	); err != nil {
		t.Fatalf("lost explicit workspace grant: %v", err)
	}
	if _, err = member.clients.Workspace.GetWorkspace(
		ctx,
		&workspacev1.GetWorkspaceRequest{WorkspaceId: o.sibling},
	); codeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("member retained inherited access: %v", err)
	}
	var count int
	err = o.api.pool.QueryRow(ctx, `SELECT count(*) FROM events
WHERE org_id=$1 AND type='org_member.role_changed'
AND subject_id=$2 AND actor_id=$2 AND data->'scopes'='["member"]'::jsonb`, o.orgID, member.id).
		Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("self-demotion audit count = %d: %v", count, err)
	}
}

func TestSetOrgMemberRoleSerializesAdminChanges(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "demote"
		if remove {
			name = "remove-versus-demote"
		}
		t.Run(name, func(t *testing.T) {
			o := newMembershipOrg(t)
			other := o.api.signUp(t, "other")
			o.addOrgMember(t, other)
			ctx := t.Context()
			if _, err := o.owner.clients.Org.SetOrgMemberRole(
				ctx,
				&orgv1.SetOrgMemberRoleRequest{
					OrgId:  o.orgID,
					UserId: other.id.String(),
					Role:   orgv1.OrgRole_ORG_ROLE_ADMIN,
				},
			); err != nil {
				t.Fatal(err)
			}
			tx, err := o.api.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rollbackOnCleanup(t, tx)
			if _, err = genDb.New(tx).LockOrgForMembership(ctx, uuid.MustParse(o.orgID)); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			results := make([]error, 2)
			for i, user := range []membershipUser{o.owner, other} {
				wg.Go(func() {
					if remove && i == 1 {
						_, results[i] = user.clients.Org.RemoveOrgMember(
							ctx,
							&orgv1.RemoveOrgMemberRequest{OrgId: o.orgID, UserId: o.owner.id.String()},
						)
					} else {
						target := user.id.String()
						if remove {
							target = other.id.String()
						}
						_, results[i] = user.clients.Org.SetOrgMemberRole(
							ctx,
							&orgv1.SetOrgMemberRoleRequest{
								OrgId:  o.orgID,
								UserId: target,
								Role:   orgv1.OrgRole_ORG_ROLE_MEMBER,
							},
						)
					}
				})
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			codes := []connect.Code{codeOf(results[0]), codeOf(results[1])}
			if results[0] != nil && results[1] != nil {
				t.Fatalf("neither change succeeded: %v", results)
			}
			if !slices.Contains(codes, connect.CodeFailedPrecondition) &&
				!slices.Contains(codes, connect.CodePermissionDenied) {
				t.Fatalf("both admin changes succeeded: %v", results)
			}
			admins, err := o.api.queries.GetUsersWithScopeOnEntity(
				ctx,
				genDb.GetUsersWithScopeOnEntityParams{
					EntityType: genDb.EntityTypeOrganization,
					EntityID:   uuid.MustParse(o.orgID),
					Scope:      genDb.ScopeAdmin,
				},
			)
			if err != nil || len(admins) != 1 {
				t.Fatalf("remaining admins = %v: %v", admins, err)
			}
		})
	}
}
