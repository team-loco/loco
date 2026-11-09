import { Code, ConnectError } from "@connectrpc/connect";

import { pluralize } from "@/lib/format";
import { agoLabel, DAY_MS } from "@/lib/time";

import { AUDIT_EVENT_TYPES } from "./auditEvents";

export const DELIVERY_TICKS = 24;

const STATUS_SUCCEEDED = "succeeded";
const STATUS_FAILED = "failed";
const STATUS_PENDING = "pending";

const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;

export type Tick = "ok" | "warn" | "err";

export interface DeliveryFields {
	id: string;
	eventType: string;
	status: string;
	attempts: number;
	lastError: string;
}

export function deliveryTick(d: DeliveryFields): Tick {
	if (d.status === STATUS_SUCCEEDED) return "ok";
	if (d.status === STATUS_FAILED) return "err";
	return "warn";
}

export function isFailing(d: DeliveryFields): boolean {
	return d.status === STATUS_FAILED || (d.status === STATUS_PENDING && d.lastError !== "");
}

export function failingCount(deliveries: readonly DeliveryFields[]): number {
	return deliveries.filter(isFailing).length;
}

export function deliveryLabel(d: DeliveryFields): string {
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

export function tickStrip(newestFirst: readonly DeliveryFields[]): TickMark[] {
	return newestFirst
		.slice(0, DELIVERY_TICKS)
		.toReversed()
		.map((d) => ({ id: d.id, tick: deliveryTick(d), tip: `${d.eventType} · ${deliveryLabel(d)}` }));
}

export function deliveryWhen(
	d: { status: string; createdMs: number; deliveredMs: number | undefined; nextMs: number | undefined },
	nowMs: number,
): string {
	if (d.status === STATUS_PENDING && d.nextMs !== undefined && d.nextMs > nowMs) {
		return `next ${inLabel(d.nextMs, nowMs)}`;
	}
	return agoLabel(d.deliveredMs ?? d.createdMs, nowMs);
}

export function inLabel(ms: number, nowMs: number): string {
	const left = ms - nowMs;
	if (left < MINUTE_MS) return "in under a minute";
	if (left < HOUR_MS) return `in ${Math.round(left / MINUTE_MS).toString()} min`;
	if (left < DAY_MS) return `in ${pluralize(Math.round(left / HOUR_MS), "hour")}`;
	return `in ${pluralize(Math.round(left / DAY_MS), "day")}`;
}

export function deliveryDetail(d: DeliveryFields): string | null {
	if (d.lastError === "" || d.status === STATUS_SUCCEEDED) return null;
	return `${d.lastError} · ${pluralize(d.attempts, "attempt")}`;
}

export function isUrlRejection(error: unknown): boolean {
	return error instanceof ConnectError && error.code === Code.InvalidArgument;
}

export function eventsSummary(types: readonly string[]): string {
	return types.length === 0 ? "All events" : pluralize(types.length, "event");
}

const GROUPS: readonly { label: string; subjects: readonly string[] }[] = [
	{ label: "Deployments", subjects: ["deployment"] },
	{ label: "Builds", subjects: ["build"] },
	{ label: "Resources", subjects: ["resource"] },
	{ label: "Domains", subjects: ["domain"] },
	{ label: "Environments", subjects: ["environment"] },
	{ label: "Secrets", subjects: ["secret"] },
	{ label: "Members", subjects: ["member"] },
	{ label: "Tokens", subjects: ["token"] },
	{ label: "Workspace", subjects: ["workspace", "webhook"] },
];

export interface EventGroup {
	label: string;
	types: string[];
}

export function webhookEventGroups(types: readonly string[] = AUDIT_EVENT_TYPES): EventGroup[] {
	return GROUPS.map((g) => ({
		label: g.label,
		types: types.filter((t) => g.subjects.includes(t.slice(0, t.indexOf(".")))),
	})).filter((g) => g.types.length > 0);
}

export type GroupState = "all" | "some" | "none";

export function groupState(selected: readonly string[], group: EventGroup): GroupState {
	const on = group.types.filter((t) => selected.includes(t)).length;
	if (on === 0) return "none";
	return on === group.types.length ? "all" : "some";
}

export function toggleGroup(selected: readonly string[], group: EventGroup): string[] {
	if (groupState(selected, group) === "all") return selected.filter((t) => !group.types.includes(t));
	return [...selected, ...group.types.filter((t) => !selected.includes(t))];
}

export function toggleEvent(selected: readonly string[], type: string): string[] {
	return selected.includes(type) ? selected.filter((t) => t !== type) : [...selected, type];
}
