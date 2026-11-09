import { expect, test } from "bun:test";
import { Code, ConnectError } from "@connectrpc/connect";

import {
	DELIVERY_TICKS,
	deliveryDetail,
	deliveryLabel,
	deliveryWhen,
	eventsSummary,
	failingCount,
	groupState,
	isUrlRejection,
	tickStrip,
	toggleEvent,
	toggleGroup,
	webhookEventGroups,
} from "./webhooks";

const delivery = (id: string, status: string, lastError = "", attempts = 1) => ({
	id,
	eventType: "deployment.created",
	status,
	attempts,
	lastError,
});

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
	expect(
		deliveryWhen({ status: "pending", createdMs: created, deliveredMs: undefined, nextMs: now + 2 * 60_000 }, now),
	).toBe("next in 2 min");
	expect(deliveryWhen({ status: "pending", createdMs: created, deliveredMs: undefined, nextMs: now - 1 }, now)).toBe(
		"10 min ago",
	);
	expect(deliveryWhen({ status: "succeeded", createdMs: created, deliveredMs: now - 60_000, nextMs: now }, now)).toBe(
		"1 min ago",
	);
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

test("selects and clears a whole group", () => {
	const group = { label: "Builds", types: ["build.created", "build.failed"] };
	expect(groupState([], group)).toBe("none");
	expect(groupState(["build.failed"], group)).toBe("some");
	const all = toggleGroup(["token.created", "build.failed"], group);
	expect(all).toEqual(["token.created", "build.failed", "build.created"]);
	expect(groupState(all, group)).toBe("all");
	expect(toggleGroup(all, group)).toEqual(["token.created"]);
	expect(toggleEvent(["build.failed"], "build.failed")).toEqual([]);
	expect(toggleEvent([], "build.failed")).toEqual(["build.failed"]);
});
