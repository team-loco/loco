import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { auditActor, auditLabel, typesIn, distinctAuditEvents } from "./auditEvents";

test("appends older audit rows once while preserving the first loaded record", () => {
	const newest = create(EventSchema, { id: "new", seq: 5n });
	const older = create(EventSchema, { id: "old", seq: 4n });
	const duplicate = create(EventSchema, { id: "new", seq: 5n, actorName: "Changed name" });
	expect(distinctAuditEvents([newest, duplicate, older])).toEqual([newest, older]);
	expect(newest.actorName).toBe("");
});

test("uses the returned actor identity without organization members", () => {
	const actor = create(EventSchema, {
		actorType: "user",
		actorName: "Wren Workspace",
		actorEmail: "wren@example.test",
	});
	expect(auditActor(actor, new Map())).toEqual({ name: "Wren Workspace", detail: "wren@example.test" });
	expect(auditActor(create(EventSchema, { actorType: "user" }), new Map())).toEqual({
		name: "Deleted user",
		detail: "",
	});
});

test("role changes appear in the access audit filter", () => {
	expect(auditLabel("org_member.role_changed")).toBe("Changed an organization member’s role");
	expect(typesIn("access")).toContain("org_member.role_changed");
});

test("platform domains read as public or internal and only user domains read as custom", () => {
	const platform = { domainSource: "platform_provided", primary: true };
	expect(auditLabel("domain.created", platform)).toBe("Made a resource public");
	expect(auditLabel("domain.deleted", platform)).toBe("Made a resource internal");
	expect(auditLabel("domain.created", { ...platform, primary: false })).toBe("Added a platform domain");
	expect(auditLabel("domain.created", { domainSource: "user_provided", primary: false })).toBe("Added a custom domain");
	expect(auditLabel("domain.deleted", { domainSource: "user_provided" })).toBe("Removed a custom domain");
	expect(auditLabel("domain.created")).toBe("Added a domain");
});
