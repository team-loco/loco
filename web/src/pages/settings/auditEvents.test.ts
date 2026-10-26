import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { auditActor, distinctAuditEvents } from "./auditEvents";

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
