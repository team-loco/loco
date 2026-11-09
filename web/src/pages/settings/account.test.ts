import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";

import { OrganizationSchema } from "@gen/loco/org/v1/org_pb";
import { EntityScopeSchema, EntityType, Scope } from "@gen/loco/token/v1/token_pb";

import { accountBlockers } from "./account";

const ME = "user-1";

test("accountBlockers lists the organizations the user created and each workspace they hold a grant on", () => {
	const mine = create(OrganizationSchema, { id: "org-1", name: "mine", createdBy: ME });
	const theirs = create(OrganizationSchema, { id: "org-2", name: "theirs", createdBy: "user-2" });
	const scopes = [
		create(EntityScopeSchema, { scope: Scope.ADMIN, entityType: EntityType.ORGANIZATION, entityId: "org-2" }),
		create(EntityScopeSchema, { scope: Scope.READ, entityType: EntityType.WORKSPACE, entityId: "ws-1" }),
		create(EntityScopeSchema, { scope: Scope.WRITE, entityType: EntityType.WORKSPACE, entityId: "ws-1" }),
		create(EntityScopeSchema, { scope: Scope.READ, entityType: EntityType.RESOURCE, entityId: "res-1" }),
	];

	const blockers = accountBlockers(ME, [mine, theirs], scopes);

	expect(blockers.orgs.map((o) => o.id)).toEqual(["org-1"]);
	expect(blockers.workspaceIds).toEqual(["ws-1"]);
});

test("accountBlockers is empty for a member of an organization they did not create", () => {
	const theirs = create(OrganizationSchema, { id: "org-2", createdBy: "user-2" });
	const scopes = [
		create(EntityScopeSchema, { scope: Scope.WRITE, entityType: EntityType.ORGANIZATION, entityId: "org-2" }),
	];

	expect(accountBlockers(ME, [theirs], scopes)).toEqual({ orgs: [], workspaceIds: [] });
});
