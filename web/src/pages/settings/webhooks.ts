import { Code, ConnectError } from "@connectrpc/connect";
import type { WebhookDelivery } from "@gen/loco/webhook/v1/webhook_pb";

import { pluralize } from "@/lib/format";
import { agoLabel, DAY_MS, maybeTsMs, tsMs } from "@/lib/time";

import { AUDIT_EVENT_TYPES } from "./auditEvents";

export const DELIVERY_TICKS = 24;

export const STATUS_SUCCEEDED = "succeeded";
export const STATUS_FAILED = "failed";
export const STATUS_PENDING = "pending";

const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;

export type Tick = "ok" | "warn" | "err";

export function deliveryTick(d: WebhookDelivery): Tick {
	if (d.status === STATUS_SUCCEEDED) return "ok";
	if (d.status === STATUS_FAILED) return "err";
	return "warn";
}

export function errorCount(deliveries: readonly WebhookDelivery[]): number {
	return deliveries.filter((d) => d.status === STATUS_FAILED || d.status === STATUS_PENDING).length;
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
		return `next ${inLabel(nextMs, nowMs)}`;
	}
	return agoLabel(maybeTsMs(d.deliveredAt) ?? tsMs(d.createdAt), nowMs);
}

function inLabel(ms: number, nowMs: number): string {
	const left = ms - nowMs;
	if (left < MINUTE_MS) return "in under a minute";
	if (left < HOUR_MS) return `in ${Math.round(left / MINUTE_MS).toString()} min`;
	if (left < DAY_MS) return `in ${pluralize(Math.round(left / HOUR_MS), "hour")}`;
	return `in ${pluralize(Math.round(left / DAY_MS), "day")}`;
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

function isRuntimeAlert(type: string): boolean {
	return RUNTIME_ALERTS.has(type);
}

function bySubject(...subjects: string[]): (type: string) => boolean {
	return (type) => !isRuntimeAlert(type) && subjects.includes(type.slice(0, type.indexOf(".")));
}

const GROUPS: readonly { label: string; match: (type: string) => boolean }[] = [
	{ label: "Runtime alerts", match: isRuntimeAlert },
	{ label: "Deployments", match: bySubject("deployment") },
	{ label: "Builds", match: bySubject("build") },
	{ label: "Resources", match: bySubject("resource") },
	{ label: "Domains", match: bySubject("domain") },
	{ label: "Environments", match: bySubject("environment") },
	{ label: "Secrets", match: bySubject("secret") },
	{ label: "Members", match: bySubject("member") },
	{ label: "Tokens", match: bySubject("token") },
	{ label: "Workspace", match: bySubject("workspace", "webhook") },
];

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
		types: types.filter(g.match),
	})).filter((g) => g.types.length > 0);
}
