import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { Code, ConnectError } from "@connectrpc/connect";
import { WebhookDeliverySchema } from "@gen/loco/webhook/v1/webhook_pb";

import { msToTimestamp } from "@/lib/time";

import {
	DELIVERY_TICKS,
	deliveryDetail,
	deliveryLabel,
	deliveryWhen,
	eventsSummary,
	failingCount,
	groupCountLabel,
	isUrlRejection,
	tickStrip,
	webhookEventGroups,
} from "./webhooks";

const delivery = (id: string, status: string, lastError = "", attempts = 1) =>
	create(WebhookDeliverySchema, { id, eventType: "deployment.created", status, attempts, lastError });

test("colours ticks oldest first and caps the strip", () => {
	const newestFirst = [
		delivery("c", "failed", "endpoint returned 503"),
		delivery("b", "pending", "timeout"),
		delivery("a", "succeeded"),
	];
	expect(tickStrip(newestFirst).map((t) => [t.id, t.tick])).toEqual([
		["a", "ok"],
		["b", "warn"],
		["c", "err"],
	]);
	const many = Array.from({ length: DELIVERY_TICKS + 1 }, (_, i) => delivery(String(i), "succeeded"));
	const strip = tickStrip(many);
	expect(strip).toHaveLength(DELIVERY_TICKS);
	expect(strip.at(-1)?.id).toBe("0");
});

test("counts failed and retrying deliveries but not queued ones", () => {
	const deliveries = [
		delivery("a", "succeeded"),
		delivery("b", "pending"),
		delivery("c", "pending", "timeout"),
		delivery("d", "failed", "endpoint returned 500"),
	];
	expect(failingCount(deliveries)).toBe(2);
	expect(deliveries.map(deliveryLabel)).toEqual(["Delivered", "Pending", "Retrying", "Failed"]);
});

test("shows the next attempt for a pending retry and the age otherwise", () => {
	const created = Date.UTC(2026, 10, 9);
	const now = created + 10 * 60_000;
	const at = (status: string, nextMs: number, deliveredMs?: number) =>
		create(WebhookDeliverySchema, {
			status,
			createdAt: msToTimestamp(created),
			nextAttemptAt: msToTimestamp(nextMs),
			...(deliveredMs === undefined ? {} : { deliveredAt: msToTimestamp(deliveredMs) }),
		});
	expect(deliveryWhen(at("pending", now + 2 * 60_000), now)).toBe("next in 2 min");
	expect(deliveryWhen(at("pending", now - 1), now)).toBe("10 min ago");
	expect(deliveryWhen(at("succeeded", now, now - 60_000), now)).toBe("1 min ago");
});

test("describes the last error with the attempt count", () => {
	expect(deliveryDetail(delivery("a", "failed", "endpoint returned 503", 7))).toBe(
		"endpoint returned 503 · 7 attempts",
	);
	expect(deliveryDetail(delivery("a", "succeeded", "endpoint returned 503", 2))).toBeNull();
	expect(deliveryDetail(delivery("a", "pending"))).toBeNull();
});

test("places only argument rejections under the URL field", () => {
	expect(isUrlRejection(new ConnectError("use an https URL with a host name", Code.InvalidArgument))).toBe(true);
	expect(isUrlRejection(new ConnectError("a workspace can have at most 10 webhooks", Code.FailedPrecondition))).toBe(
		false,
	);
	expect(isUrlRejection(new Error("network down"))).toBe(false);
});

test("summarises event filters", () => {
	expect(eventsSummary([])).toBe("All events");
	expect(eventsSummary(["build.failed"])).toBe("1 event");
	expect(eventsSummary(["build.failed", "build.succeeded"])).toBe("2 events");
});

test("counts a group's chosen events as all or n/m", () => {
	expect(groupCountLabel(5, 5)).toBe("all");
	expect(groupCountLabel(4, 5)).toBe("4/5");
	expect(groupCountLabel(0, 5)).toBe("0/5");
});

test("groups workspace events by subject and leaves out organization events", () => {
	const groups = webhookEventGroups([
		"org.created",
		"org_member.added",
		"member.added",
		"webhook.created",
		"workspace.updated",
	]);
	expect(groups).toEqual([
		{ label: "Members", types: ["member.added"] },
		{ label: "Workspace", types: ["webhook.created", "workspace.updated"] },
	]);
	const builds = webhookEventGroups().find((g) => g.label === "Builds");
	expect(builds?.types).toContain("build.failed");
});
