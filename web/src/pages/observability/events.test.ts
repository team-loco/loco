import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { runtimeEvent } from "@/lib/runtime-events";
import { expect, test } from "bun:test";
import {
	eventDataLabel,
	eventReasonLabel,
	eventItem,
	eventJson,
	eventSummary,
	eventTitle,
	eventValue,
	severityOf,
	eventWindow,
} from "./events";
test("degraded resources have warning severity", () => {
	expect(severityOf("resource.degraded")).toBe("warning");
	expect(severityOf("resource.recovered")).toBe("normal");
});
test("build failures have error severity", () => {
	expect(severityOf("build.failed")).toBe("error");
	expect(severityOf("resource.restarted")).toBe("normal");
});

test("event titles preserve unknown types and include regions", () => {
	expect(eventTitle("custom.event", "")).toBe("custom.event");
	expect(eventTitle("resource.degraded", "us-east-1")).toBe("Degraded in us-east-1");
});

test("relative ranges retain received events between clock ticks", () => {
	const range = { kind: "relative", spanMs: 60000 } as const;
	expect(eventWindow(range, 100000, 100500)).toEqual({ from: 40500, to: 100500 });
	expect(eventWindow(range, 100100, 100500)).toEqual({ from: 40500, to: 100500 });
	expect(eventWindow(range, 101000, 100500)).toEqual({ from: 41000, to: 101000 });
	expect(eventWindow({ kind: "absolute", from: 1000, to: 2000 }, 100000, 100500)).toEqual({
		from: 1000,
		to: 2000,
	});
});

test("retains event context for filters and raw JSON", () => {
	const event = eventItem({
		...runtimeEvent(
			create(EventSchema, {
				id: "event",
				seq: 17n,
				type: "deployment.ready",
				actorType: "user",
				actorName: "Olivia",
				actorEmail: "owner@acme.test",
				subjectType: "deployment",
				requestId: "req-1",
				environmentId: "production",
				createdAt: { seconds: 1800000000n },
				data: { deploymentId: "abcd1234-rest", region: "us-east-1" },
			}),
		),
		resourceId: "resource",
		resourceName: "web",
	});
	expect(event.title).toBe("Deploy ready in us-east-1");
	expect(eventSummary(event)).toBe("deployment abcd1234");
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
		data: { region: "us-east-1" },
	});
	expect(severityOf("deployment.failed")).toBe("error");
	const scaled = { ...event, type: "resource.scaled", replicas: "3", region: "us-east-1", message: "" };
	expect(eventSummary(scaled)).toBe("replicas 3 · region us-east-1");
	expect(eventSummary({ ...scaled, message: "Scaled by operator" })).toBe("Scaled by operator");
});

test("labels degraded reasons while keeping filter values raw", () => {
	const event = eventItem({
		...runtimeEvent(create(EventSchema, { type: "resource.degraded", data: { reason: "crash_looping" } })),
		resourceId: "resource",
		resourceName: "web",
	});
	expect(eventSummary(event)).toBe("Restarting after crash");
	expect(eventReasonLabel("out_of_memory")).toBe("Out of memory");
	expect(eventValue(event, "reason")).toBe("crash_looping");
	expect(eventSummary({ ...event, message: "Restart backoff" })).toBe("Restart backoff");
	expect(eventDataLabel("deploymentId")).toBe("deployment_id");
	expect(eventDataLabel("actor_type")).toBe("actor_type");
});
