import { Code, ConnectError } from "@connectrpc/connect";
import type { WebhookDelivery } from "@gen/loco/webhook/v1/webhook_pb";

import type { Glyph } from "@/components/design/StatusGlyph";
import { pluralize } from "@/lib/format";
import { maybeTsMs, shortAgoLabel, shortDurationLabel, tsMs } from "@/lib/time";

import { AUDIT_EVENT_TYPES } from "../settings/auditEvents";

export const DELIVERY_TICKS = 24;

export const STATUS_SUCCEEDED = "succeeded";
export const STATUS_FAILED = "failed";
export const STATUS_PENDING = "pending";

export type Tick = "ok" | "warn" | "err" | "wait";

export function deliveryTick(d: WebhookDelivery): Tick {
	if (d.status === STATUS_SUCCEEDED) return "ok";
	if (d.status === STATUS_FAILED) return "err";
	return d.lastError === "" ? "wait" : "warn";
}

export function deliveryGlyph(d: WebhookDelivery): Glyph {
	const tick = deliveryTick(d);
	return tick === "wait" ? "off" : tick;
}

export function deliveryLabel(d: WebhookDelivery): string {
	if (d.status === STATUS_SUCCEEDED) return "Delivered";
	if (d.status === STATUS_FAILED) return "Failed";
	if (d.status === STATUS_PENDING) return d.lastError === "" ? "Pending" : "Retrying";
	return d.status;
}

export interface TickMark {
	id: string;
	tick: Tick;
	tip: string;
}

export function tickStrip(newestFirst: readonly WebhookDelivery[]): TickMark[] {
	return newestFirst
		.slice(0, DELIVERY_TICKS)
		.toReversed()
		.map((d) => ({ id: d.id, tick: deliveryTick(d), tip: `${d.eventType} · ${deliveryLabel(d)}` }));
}

export function deliveryWhen(d: WebhookDelivery, nowMs: number): string {
	const nextMs = maybeTsMs(d.nextAttemptAt);
	if (d.status === STATUS_PENDING && nextMs !== undefined && nextMs > nowMs) {
		return `retry in ${shortDurationLabel(nextMs - nowMs)}`;
	}
	return shortAgoLabel(maybeTsMs(d.deliveredAt) ?? tsMs(d.createdAt), nowMs);
}

export function isPrivateAddress(value: string): boolean {
	if (!URL.canParse(value)) return false;
	const host = new URL(value).hostname.toLowerCase();
	if (host === "localhost" || host.endsWith(".localhost")) return true;
	const octets = host.split(".").map(Number);
	return (
		octets.length === 4 &&
		(octets[0] === 127 ||
			octets[0] === 10 ||
			(octets[0] === 192 && octets[1] === 168) ||
			(octets[0] === 169 && octets[1] === 254) ||
			(octets[0] === 172 && octets[1] !== undefined && octets[1] >= 16 && octets[1] <= 31))
	);
}

export function deliveryDetail(d: WebhookDelivery): string | null {
	if (d.lastError === "" || d.status === STATUS_SUCCEEDED) return null;
	return `${d.lastError} · ${pluralize(d.attempts, "attempt")}`;
}

export function isHttpsUrl(value: string): boolean {
	return URL.canParse(value) && new URL(value).protocol === "https:";
}

export function isUrlRejection(error: unknown): boolean {
	return error instanceof ConnectError && error.code === Code.InvalidArgument;
}

export function eventsSummary(types: readonly string[]): string {
	return types.length === 0 ? "All events" : pluralize(types.length, "event");
}

const RUNTIME_ALERTS: ReadonlySet<string> = new Set(["resource.degraded", "resource.recovered"]);

const GROUPS: readonly { label: string; subjects: readonly string[] }[] = [
	{ label: "Runtime alerts", subjects: ["runtime"] },
	{ label: "Deployments", subjects: ["deployment"] },
	{ label: "Builds", subjects: ["build"] },
	{ label: "Resources", subjects: ["resource"] },
	{ label: "Domains", subjects: ["domain"] },
	{ label: "Environments", subjects: ["environment"] },
	{ label: "Secrets", subjects: ["secret", "secret_key"] },
	{ label: "Members", subjects: ["member"] },
	{ label: "Tokens", subjects: ["token"] },
	{ label: "Workspace", subjects: ["workspace", "webhook"] },
];

function subjectOf(type: string): string {
	return RUNTIME_ALERTS.has(type) ? "runtime" : type.slice(0, type.indexOf("."));
}

interface EventGroup {
	label: string;
	types: string[];
}

export function groupCountLabel(selected: number, total: number): string {
	return selected === total ? "all" : `${selected.toString()}/${total.toString()}`;
}

export function webhookEventGroups(types: readonly string[] = AUDIT_EVENT_TYPES): EventGroup[] {
	return GROUPS.map((g) => ({
		label: g.label,
		types: types.filter((t) => t !== "webhook.test" && g.subjects.includes(subjectOf(t))),
	})).filter((g) => g.types.length > 0);
}
