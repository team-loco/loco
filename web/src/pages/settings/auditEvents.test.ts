import { expect, test } from "bun:test";
import { create, type JsonObject } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { auditActor, auditDetail, auditLabel, typesIn, distinctAuditEvents } from "./auditEvents";

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
	expect(auditActor(actor)).toEqual({ name: "Wren Workspace", detail: "wren@example.test" });
	expect(auditActor(create(EventSchema, { actorType: "user" }))).toEqual({
		name: "Deleted user",
		detail: "",
	});
});

test("role changes appear in the access audit filter", () => {
	expect(auditLabel("org_member.role_changed")).toBe("Changed an organization member’s role");
	expect(typesIn("access")).toContain("org_member.role_changed");
});

test("app domains read as public or internal and only user domains read as custom", () => {
	const app = { domainSource: "platform_provided", primary: true };
	expect(auditLabel("domain.created", app)).toBe("Made a resource public");
	expect(auditLabel("domain.deleted", app)).toBe("Made a resource internal");
	expect(auditLabel("domain.created", { ...app, primary: false })).toBe("Added an app domain");
	expect(auditLabel("domain.created", { domainSource: "user_provided", primary: false })).toBe("Added a custom domain");
	expect(auditLabel("domain.deleted", { domainSource: "user_provided" })).toBe("Removed a custom domain");
	expect(auditLabel("domain.created")).toBe("Added a domain");
});

test("names an API token by its recorded name and owner kind, also after deletion", () => {
	const token = create(EventSchema, { actorType: "api_token", actorName: "ci", actorTokenType: "workspace" });
	expect(auditActor(token)).toEqual({ name: "ci", detail: "Workspace token" });
	expect(auditActor(create(EventSchema, { actorType: "api_token" }))).toEqual({
		name: "Deleted token",
		detail: "API token",
	});
});

test("workspace updates say what changed", () => {
	const summary = (data: JsonObject) => {
		const event = { type: "workspace.updated", subjectId: "", data };
		return `${auditLabel(event.type, data)} ${auditDetail(event, new Map()) ?? ""}`.trim();
	};
	expect(summary({ name: "acme-prod", old: { name: "acme" }, new: { name: "acme-prod" } })).toBe(
		"Renamed workspace acme → acme-prod",
	);
	const retention = (log: number | null, next: number | null) => ({
		name: "acme",
		old: { log_retention_days: log, trace_retention_days: 10, metric_retention_days: null },
		new: { log_retention_days: next, trace_retention_days: 10, metric_retention_days: null },
	});
	expect(summary(retention(7, 30))).toBe("Changed log retention 7 → 30 days");
	expect(summary(retention(7, null))).toBe("Changed log retention 7 days → org limit");
	expect(summary({ old: { description: "a" }, new: { description: "b" } })).toBe("Changed the workspace description");
	expect(
		summary({
			name: "acme-prod",
			old: { name: "acme", log_retention_days: null },
			new: { name: "acme-prod", log_retention_days: 30 },
		}),
	).toBe("Updated a workspace name acme → acme-prod, log retention org limit → 30 days");
	expect(summary({ name: "acme", old: { name: "acme" }, new: { name: "acme" } })).toBe("Updated a workspace");
});
