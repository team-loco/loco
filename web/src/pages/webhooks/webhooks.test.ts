import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { createQueryOptions } from "@connectrpc/connect-query";
import { listWebhookDeliveries } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { QueryClient } from "@tanstack/react-query";
import { WebhookDeliverySchema } from "@gen/loco/webhook/v1/webhook_pb";

import { msToTimestamp } from "@/lib/time";

import {
	DELIVERY_TICKS,
	deliveryDetail,
	deliveryLabel,
	deliveryTick,
	deliveryWhen,
	eventsSummary,
	chosenLabel,
	groupCountLabel,
	hasPending,
	isHttpsUrl,
	isPrivateAddress,
	isUrlRejection,
	refreshDeliveries,
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

test("labels delivered, fresh, retrying and failed deliveries", () => {
	const deliveries = [
		delivery("a", "succeeded"),
		delivery("b", "pending"),
		delivery("c", "pending", "timeout"),
		delivery("d", "failed", "endpoint returned 500"),
	];
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
	expect(deliveryWhen(at("pending", now + 2 * 60_000), now)).toBe("retry in 2m");
	expect(deliveryWhen(at("pending", now - 1), now)).toBe("10m ago");
	expect(deliveryWhen(at("succeeded", now, now - 60_000), now)).toBe("1m ago");
});

test("describes the last error with the attempt count", () => {
	expect(deliveryDetail(delivery("a", "failed", "endpoint returned 503", 7))).toBe("endpoint returned 503, 7 attempts");
	expect(deliveryDetail(delivery("a", "succeeded", "endpoint returned 503", 2))).toBeNull();
	expect(deliveryDetail(delivery("a", "pending"))).toBeNull();
});

test("places only argument rejections under the URL field", () => {
	expect(isUrlRejection(new ConnectError("use an https URL with a host name", Code.InvalidArgument))).toBe(true);
	expect(isUrlRejection(new ConnectError("count limit reached", Code.ResourceExhausted))).toBe(false);
	expect(isUrlRejection(new Error("network down"))).toBe(false);
});

test("summarises event filters", () => {
	expect(eventsSummary([])).toBe("All events");
	expect(eventsSummary(["build.failed"])).toBe("1 event");
	expect(eventsSummary(["build.failed", "build.succeeded"])).toBe("2 events");
});

test("counts a group's events, or its chosen events once any are chosen", () => {
	expect(groupCountLabel(0, 5)).toBe("5 events");
	expect(groupCountLabel(0, 1)).toBe("1 event");
	expect(groupCountLabel(4, 5)).toBe("4 of 5");
	expect(groupCountLabel(5, 5)).toBe("5 of 5");
});

test("summarises the chosen events", () => {
	expect(chosenLabel(0, 40)).toBe("None chosen yet");
	expect(chosenLabel(3, 40)).toBe("3 of 40 events");
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
	const deployments = webhookEventGroups().find((g) => g.label === "Deployments");
	for (const type of ["deployment.ready", "deployment.failed", "deployment.crashed"]) {
		expect(deployments?.types).toContain(type);
	}
});

test("accepts only https endpoint URLs", () => {
	expect(isHttpsUrl("https://hooks.example.com/loco")).toBe(true);
	expect(isHttpsUrl("http://hooks.example.com/loco")).toBe(false);
	expect(isHttpsUrl("hooks.example.com")).toBe(false);
});

test("puts runtime alerts in their own group", () => {
	const groups = webhookEventGroups(["resource.created", "resource.degraded", "resource.recovered"]);
	expect(groups).toEqual([
		{ label: "Runtime alerts", types: ["resource.degraded", "resource.recovered"] },
		{ label: "Resources", types: ["resource.created"] },
	]);
});

test("offers workspace secret key events without targeted test or organization events", () => {
	const groups = webhookEventGroups([
		"secret.updated",
		"secret_key.rewrapped",
		"webhook.test",
		"webhook.created",
		"invitation.created",
		"invitation.resent",
		"invitation.claimed",
		"invitation.revoked",
	]);
	expect(groups).toEqual([
		{ label: "Secrets", types: ["secret.updated", "secret_key.rewrapped"] },
		{ label: "Workspace", types: ["webhook.created"] },
	]);
	expect(webhookEventGroups().find((g) => g.label === "Secrets")?.types).toContain("secret_key.rewrapped");
	expect(webhookEventGroups().flatMap((g) => g.types)).not.toContain("webhook.test");
});

test("blocks private endpoint addresses before submission", () => {
	for (const host of ["localhost", "127.0.0.1", "10.1.2.3", "192.168.0.1", "169.254.0.1", "172.16.0.1", "172.31.255.1"])
		expect(isPrivateAddress(`https://${host}/hook`)).toBe(true);
	for (const host of ["example.com", "172.15.0.1", "172.32.0.1"])
		expect(isPrivateAddress(`https://${host}/hook`)).toBe(false);
	expect(tickStrip([delivery("pending", "pending")])[0]?.tick).toBe("off");
});

test("maps fresh pending deliveries to the neutral glyph", () => {
	expect(deliveryTick(delivery("pending", "pending"))).toBe("off");
	expect(deliveryTick(delivery("retry", "pending", "timeout"))).toBe("warn");
	expect(deliveryTick(delivery("failed", "failed"))).toBe("err");
	expect(deliveryTick(delivery("sent", "succeeded"))).toBe("ok");
});

test("refreshing deliveries invalidates the table row and drawer lists of every webhook", async () => {
	const transport = createRouterTransport(() => {});
	const client = new QueryClient();
	const row = createQueryOptions(listWebhookDeliveries, { workspaceId: "ws", webhookId: "a" }, { transport });
	const other = createQueryOptions(listWebhookDeliveries, { workspaceId: "ws", webhookId: "b" }, { transport });
	client.setQueryData(row.queryKey, {});
	client.setQueryData(other.queryKey, {});
	await refreshDeliveries(client);
	expect(client.getQueryState(row.queryKey)?.isInvalidated).toBe(true);
	expect(client.getQueryState(other.queryKey)?.isInvalidated).toBe(true);
	client.clear();
});

test("polls only while a delivery is pending", () => {
	expect(hasPending(undefined)).toBe(false);
	expect(hasPending([delivery("a", "succeeded"), delivery("b", "failed")])).toBe(false);
	expect(hasPending([delivery("a", "succeeded"), delivery("b", "pending")])).toBe(true);
});
