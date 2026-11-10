import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityScopeSchema, EntityType, Scope } from "@gen/loco/token/v1/token_pb";
import { describe, expect, test } from "bun:test";

import { resourceMissing, workspaceKnown } from "./not-found";

const WS = "ws-1";

function scope(entityType: EntityType, entityId: string) {
	return create(EntityScopeSchema, { entityType, entityId, scope: Scope.READ });
}

describe("resourceMissing", () => {
	test("NotFound is missing", () => {
		expect(resourceMissing(new ConnectError("gone", Code.NotFound), false)).toBe(true);
	});

	test("PermissionDenied is missing when the user can read the workspace", () => {
		expect(resourceMissing(new ConnectError("denied", Code.PermissionDenied), true)).toBe(true);
	});

	test("PermissionDenied keeps the error without workspace read", () => {
		expect(resourceMissing(new ConnectError("denied", Code.PermissionDenied), false)).toBe(false);
	});

	test("other errors keep the error", () => {
		expect(resourceMissing(new ConnectError("down", Code.Unavailable), true)).toBe(false);
		expect(resourceMissing(new ConnectError("bad", Code.InvalidArgument), true)).toBe(false);
		expect(resourceMissing(new Error("network"), true)).toBe(false);
	});
});

describe("workspaceKnown", () => {
	test("a workspace in the user's list is known", () => {
		expect(workspaceKnown(WS, [{ id: WS }], [])).toBe(true);
	});

	test("a workspace grant makes it known without the list", () => {
		expect(workspaceKnown(WS, [], [scope(EntityType.WORKSPACE, WS)])).toBe(true);
	});

	test("any resource grant makes it known", () => {
		expect(workspaceKnown(WS, [], [scope(EntityType.RESOURCE, "res-1")])).toBe(true);
	});

	test("anything else is unknown", () => {
		expect(
			workspaceKnown(WS, [{ id: "ws-2" }], [scope(EntityType.WORKSPACE, "ws-2"), scope(EntityType.ORGANIZATION, "o")]),
		).toBe(false);
	});
});
