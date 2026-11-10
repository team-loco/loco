import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityScopeSchema, EntityType, Scope } from "@gen/loco/token/v1/token_pb";
import { describe, expect, spyOn, test } from "bun:test";

import { logCaughtError, notFound, resourceMissing, workspaceKnown } from "./not-found";

const ORG = "org-1";
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
		expect(workspaceKnown(ORG, WS, [{ id: WS }], [])).toBe(true);
	});

	test("a workspace grant makes it known without the list", () => {
		expect(workspaceKnown(ORG, WS, [], [scope(EntityType.WORKSPACE, WS)])).toBe(true);
	});

	test("any resource grant makes it known", () => {
		expect(workspaceKnown(ORG, WS, [], [scope(EntityType.RESOURCE, "res-1")])).toBe(true);
	});

	test("membership in the org makes every workspace in it known", () => {
		expect(
			workspaceKnown(
				ORG,
				WS,
				[],
				[create(EntityScopeSchema, { entityType: EntityType.ORGANIZATION, entityId: ORG, scope: Scope.MEMBER })],
			),
		).toBe(true);
	});

	test("anything else is unknown", () => {
		expect(
			workspaceKnown(
				ORG,
				WS,
				[{ id: "ws-2" }],
				[scope(EntityType.WORKSPACE, "ws-2"), scope(EntityType.ORGANIZATION, "org-2")],
			),
		).toBe(false);
	});
});

describe("logCaughtError", () => {
	test("logs caught errors except the not-found page", () => {
		const log = spyOn(console, "error").mockImplementation(() => undefined);
		const info = { componentStack: "\n    at Page" };
		try {
			notFound();
		} catch (error) {
			logCaughtError(error, info);
		}
		expect(log).not.toHaveBeenCalled();
		const failure = new Error("render failed");
		logCaughtError(failure, info);
		expect(log).toHaveBeenCalledWith(failure, info.componentStack);
		log.mockRestore();
	});
});
