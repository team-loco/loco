import type { Event } from "@gen/loco/event/v1/event_pb";

import { DAY_MS, tsMs } from "@/lib/time";
import {
	AUDIT_CATEGORIES,
	auditActor,
	auditDetail,
	auditLabel,
	type AuditActor,
	type AuditUser,
	typesIn,
} from "@/pages/settings/auditEvents";

export const RANGES = [
	{ value: "24h", label: "Past 24 hours", ms: DAY_MS },
	{ value: "7d", label: "Past 7 days", ms: 7 * DAY_MS },
	{ value: "30d", label: "Past 30 days", ms: 30 * DAY_MS },
	{ value: "90d", label: "Past 90 days", ms: 90 * DAY_MS },
] as const;

export type Range = (typeof RANGES)[number];

export const DEFAULT_RANGE: Range = RANGES[1];

export const PAGE_SIZES = [50, 100, 200] as const;

export const CATEGORIES = AUDIT_CATEGORIES.flatMap((c) =>
	c.value === "all" ? [] : [{ value: c.value, label: c.label, types: typesIn(c.value) }],
);

/** The Event filter button text: one type, whole categories by name, or the first type and a count. */
export function typesLabel(types: readonly string[]): string {
	const [first] = types;
	if (first === undefined) return "";
	if (types.length === 1) return auditLabel(first);
	const whole = CATEGORIES.filter((c) => c.types.every((t) => types.includes(t)));
	if (whole.reduce((n, c) => n + c.types.length, 0) === types.length) return whole.map((c) => c.label).join(", ");
	return `${auditLabel(first)} +${(types.length - 1).toString()}`;
}

export function toggleType(types: readonly string[], type: string): string[] {
	return types.includes(type) ? types.filter((t) => t !== type) : [...types, type];
}

/** Selects every type in the category, or clears them when all are already selected. */
export function toggleCategory(types: readonly string[], category: readonly string[]): string[] {
	const rest = types.filter((t) => !category.includes(t));
	return category.every((t) => types.includes(t)) ? rest : [...rest, ...category];
}

export function dayLabel(ms: number, nowMs: number): string {
	const day = new Date(ms).toDateString();
	if (day === new Date(nowMs).toDateString()) return "Today";
	if (day === new Date(nowMs - DAY_MS).toDateString()) return "Yesterday";
	return new Date(ms).toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });
}

export const ACTOR_KINDS: Record<string, string> = {
	user: "Person",
	organization: "Organization token",
	workspace: "Workspace token",
	resource: "Resource token",
	system: "Loco",
	agent: "Cluster agent",
	anonymous: "Anonymous",
};

/** The Actor filter selects these by type: Loco and anonymous actors have no ID, and cluster agents are one choice. */
export const TYPE_ACTORS = ["system", "agent", "anonymous"];

// Error receipts copy complete UUIDs minted in internal/rpc/requestid.go.
export function requestIdFilter(text: string): string {
	const value = text.trim();
	return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value) ? value : "";
}

export function matchesPageFilter(row: AuditRow, text: string): boolean {
	const query = text.trim().toLowerCase();
	return [
		row.actor.name,
		row.actor.detail,
		row.label,
		row.detail,
		row.where,
		row.environment,
		row.event.requestId,
	].some((value) => value.toLowerCase().includes(query));
}

export interface AuditRow {
	event: Event;
	ms: number;
	actor: AuditActor;
	actorKind: string;
	label: string;
	detail: string;
	where: string;
	resource: string;
	environment: string;
}

/** `names` maps the organization, workspace, resource and environment IDs the caller can read to their names. */
export function auditRow(
	event: Event,
	users: ReadonlyMap<string, AuditUser>,
	names: ReadonlyMap<string, string>,
): AuditRow {
	const name = (id: string) => names.get(id) ?? id.slice(0, 8);
	const workspace = event.workspaceId === "" ? "Organization" : name(event.workspaceId);
	const resource = event.resourceId === "" ? "" : name(event.resourceId);
	return {
		event,
		ms: tsMs(event.createdAt),
		actor: auditActor(event, names),
		actorKind: ACTOR_KINDS[event.actorType] ?? event.actorType,
		label: auditLabel(event.type),
		detail: auditDetail(event, users) ?? "",
		where: resource === "" ? workspace : `${workspace} / ${resource}`,
		resource,
		environment: event.environmentId === "" ? "" : name(event.environmentId),
	};
}
