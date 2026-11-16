import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { expect, test } from "bun:test";

import { typesIn } from "@/pages/settings/auditEvents";

import { auditQuery, auditRow, toggleCategory, toggleType, typesLabel } from "./model";

test("the event filter names one type, whole categories, or the first type and a count", () => {
	expect(typesLabel([])).toBe("");
	expect(typesLabel(["token.created"])).toBe("Created an API token");
	expect(typesLabel([...typesIn("tokens"), ...typesIn("access")])).toBe("Access, Tokens");
	expect(typesLabel(["token.created", "deployment.created"])).toBe("Created an API token +1");
});

test("a category toggle selects every type in it, then clears them", () => {
	const tokens = typesIn("tokens");
	const some = toggleType([], "token.created");
	expect(toggleCategory(some, tokens).toSorted()).toEqual(tokens.toSorted());
	expect(toggleCategory(tokens, tokens)).toEqual([]);
	expect(toggleType(some, "token.created")).toEqual([]);
});

test("the toolbar filters and the search text become one audit query", () => {
	const id = "0198f2a4-6c1e-7c3b-9d4e-2f6a1b3c5d7e";
	expect(auditQuery([], "", "  ")).toBe("");
	expect(auditQuery([], "", "ada OR shop")).toBe("ada OR shop");
	expect(auditQuery(["token.created", "token.revoked"], "system", "")).toBe(
		"(@type:token.created OR @type:token.revoked) @actor_type:system",
	);
	expect(auditQuery([], id, " ada -@type:secret.* ")).toBe(`@actor:${id} ada -@type:secret.*`);
	expect(auditQuery(["token.created"], "", "ada OR shop")).toBe("(@type:token.created) (ada OR shop)");
});

test("rows name where an event happened from the IDs the caller can read", () => {
	const names = new Map([
		["w1", "storefront"],
		["r1", "web"],
		["e1", "production"],
	]);
	const deploy = create(EventSchema, {
		type: "deployment.created",
		workspaceId: "w1",
		resourceId: "r1",
		environmentId: "e1",
		actorType: "workspace",
		actorId: "w1",
	});
	expect(auditRow(deploy, new Map(), names)).toMatchObject({
		where: "storefront / web",
		resource: "web",
		environment: "production",
		actorKind: "Workspace token",
		actor: { name: "Workspace token", detail: "storefront" },
	});
	const gone = create(EventSchema, { type: "resource.deleted", workspaceId: "w1", resourceId: "0199aaaa-bbbb" });
	expect(auditRow(gone, new Map(), names).where).toBe("storefront / 0199aaaa");
	expect(auditRow(create(EventSchema, { type: "org.updated" }), new Map(), names).where).toBe("Organization");
});
