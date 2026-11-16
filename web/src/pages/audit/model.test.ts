import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { describe, expect, test } from "bun:test";

import { typesIn } from "@/pages/settings/auditEvents";

import {
	auditRow,
	filtersQuery,
	matchesPageFilter,
	requestIdFilter,
	toggleCategory,
	toggleType,
	typesLabel,
} from "./model";

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

test("the toolbar filters become one audit query", () => {
	const id = "0198f2a4-6c1e-7c3b-9d4e-2f6a1b3c5d7e";
	expect(filtersQuery([], "", "")).toBe("");
	expect(filtersQuery(["token.created", "token.revoked"], "system", "")).toBe(
		"(@type:token.created OR @type:token.revoked) @actor_type:system",
	);
	expect(filtersQuery([], id, id)).toBe(`@actor:${id} @request:${id}`);
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

describe("audit page filtering", () => {
	test("only complete request UUIDs trigger history lookup", () => {
		const id = "01900000-0000-7000-8000-000000000001";
		expect(requestIdFilter(` ${id} `)).toBe(id);
		for (const text of ["", "Ada", "checkout", id.slice(0, 12), "%"]) expect(requestIdFilter(text)).toBe("");
	});
	test("matches displayed row values without a minimum length", () => {
		const row = auditRow(
			create(EventSchema, {
				type: "resource.created",
				actorType: "user",
				actorName: "Ada",
				actorEmail: "ada@example.test",
				requestId: "01900000-0000-7000-8000-000000000001",
				data: { name: "Checkout" },
			}),
			new Map(),
			new Map(),
		);
		for (const text of ["", "a", "ADA", "example", "Created a resource", "checkout", row.event.requestId])
			expect(matchesPageFilter(row, text)).toBe(true);
		expect(matchesPageFilter(row, "no match")).toBe(false);
	});
});
