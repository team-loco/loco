import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { runtimeEvent } from "@/lib/runtime-events";
import { expect, test } from "bun:test";
import { eventDataLabel, eventItem, eventJson, eventSummary, asSeverity, eventValue } from "./events";
test("unknown severities show as normal", () => {
	expect(asSeverity("warning")).toBe("warning");
	expect(asSeverity("error")).toBe("error");
	expect(asSeverity("")).toBe("normal");
});

test("retains event context for filters and raw JSON", () => {
	const event = eventItem({
		...runtimeEvent(
			create(EventSchema, {
				id: "event",
				seq: 17n,
				type: "deployment.ready",
				title: "Deploy ready in us-va-1",
				severity: "normal",
				region: "us-va-1",
				actorType: "user",
				actorName: "Olivia",
				actorEmail: "owner@acme.test",
				subjectType: "deployment",
				requestId: "req-1",
				environmentId: "production",
				createdAt: { seconds: 1800000000n },
				data: { deploymentId: "019ab668-1111-7000-8000-000011112222", region: "us-va-1" },
			}),
		),
		resourceId: "resource",
		resourceName: "web",
	});
	expect(event.title).toBe("Deploy ready in us-va-1");
	expect(eventSummary(event)).toBe("deployment 11112222");
	expect(eventValue(event, "actor")).toBe("Olivia");
	expect(eventValue(event, "severity")).toBe("normal");
	expect(eventValue(event, "type")).toBe("deployment.ready");
	expect(event.environmentId).toBe("production");
	expect(JSON.parse(eventJson(event))).toMatchObject({
		seq: "17",
		id: "event",
		actor_type: "user",
		actor_name: "Olivia",
		subject_type: "deployment",
		request_id: "req-1",
		data: { region: "us-va-1" },
	});
	const scaled = { ...event, type: "resource.scaled", replicas: "3", region: "us-va-1", message: "" };
	expect(eventSummary(scaled)).toBe("replicas 3 · region us-va-1");
	expect(eventSummary({ ...scaled, message: "Scaled by operator" })).toBe("Scaled by operator");
});

test("shows a degraded event's message, or its raw reason without one", () => {
	const event = eventItem({
		...runtimeEvent(create(EventSchema, { type: "resource.degraded", data: { reason: "restarting" } })),
		resourceId: "resource",
		resourceName: "web",
	});
	expect(eventSummary(event)).toBe("restarting");
	expect(eventValue(event, "reason")).toBe("restarting");
	expect(eventSummary({ ...event, message: "Restart backoff" })).toBe("Restart backoff");
	expect(eventDataLabel("deploymentId")).toBe("deployment_id");
	expect(eventDataLabel("actor_type")).toBe("actor_type");
});
