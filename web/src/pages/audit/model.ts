import type { Event } from "@gen/loco/event/v1/event_pb";

import { eventTypesQuery } from "@/lib/audit-query";
import { hasBoolean } from "@/pages/observability/query";
import { DAY_MS, formatIsoDate, formatWeekdayDate, previousDay, type TimePreferences, tsMs } from "@/lib/time";
import {
	AUDIT_CATEGORIES,
	auditActor,
	auditDetail,
	auditLabel,
	text as dataText,
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

export function dayLabel(ms: number, nowMs: number, pref: TimePreferences): string {
	const day = formatIsoDate(ms, pref);
	if (day === formatIsoDate(nowMs, pref)) return "Today";
	if (day === formatIsoDate(previousDay(nowMs, pref), pref)) return "Yesterday";
	return formatWeekdayDate(ms, pref);
}

export const ACTOR_KINDS: Record<string, string> = {
	user: "Person",
	system: "Loco",
	agent: "Cluster agent",
};

/** The Actor filter selects these by type: Loco has no actor ID, and cluster agents are one choice. */
export const TYPE_ACTORS = ["system", "agent"];

/**
 * The audit query for the toolbar filters and the search text; the actor is an actor ID or one of
 * TYPE_ACTORS. Search text that uses AND, OR or parentheses is grouped, so the filters apply to all of it.
 */
export function auditQuery(types: readonly string[], actor: string, search: string): string {
	const actorField = TYPE_ACTORS.includes(actor) ? "actor_type" : "actor";
	const filters = [eventTypesQuery(types), actor === "" ? "" : `@${actorField}:${actor}`].filter((p) => p !== "");
	const text = search.trim();
	return [...filters, filters.length > 0 && hasBoolean(text) ? `(${text})` : text].filter((p) => p !== "").join(" ");
}

export interface AuditRow {
	event: Event;
	ms: number;
	actor: AuditActor;
	label: string;
	detail: string;
	/** The organization name, or "Installation" for an event that belongs to none. */
	org: string;
	where: string;
	resource: string;
	environment: string;
}

function orgLabel(event: Event, names: ReadonlyMap<string, string>): string {
	return event.orgName || (names.get(event.orgId) ?? event.orgId.slice(-8));
}

/** `names` maps the organization, workspace, resource and environment IDs the caller can read to their names. */
export function auditRow(
	event: Event,
	users: ReadonlyMap<string, AuditUser>,
	names: ReadonlyMap<string, string>,
): AuditRow {
	// A deleted resource has no live name; an event about it records the name it had.
	const name = (id: string) =>
		names.get(id) ?? (event.subjectId === id ? dataText(event.data, "name") : undefined) ?? id.slice(0, 8);
	const workspace = event.workspaceId === "" ? (event.orgId === "" ? "" : "Organization") : name(event.workspaceId);
	const resource = event.resourceId === "" ? "" : name(event.resourceId);
	return {
		event,
		ms: tsMs(event.createdAt),
		actor: auditActor(event),
		label: auditLabel(event.type, event.data),
		detail: auditDetail(event, users) ?? "",
		org: event.orgId === "" ? "Installation" : orgLabel(event, names),
		where: resource === "" ? workspace : `${workspace} / ${resource}`,
		resource,
		environment: event.environmentId === "" ? "" : name(event.environmentId),
	};
}

export interface OrgChoice {
	id: string;
	name: string;
}

/** The organization filter's choices: the listed organizations, then any other organization the shown events name that matches the search. */
export function orgFilterItems(listed: readonly OrgChoice[], rows: readonly AuditRow[], search: string): OrgChoice[] {
	const needle = search.trim().toLowerCase();
	const items = [...listed];
	for (const r of rows) {
		const id = r.event.orgId;
		const matches = r.org.toLowerCase().includes(needle);
		if (id !== "" && matches && !items.some((o) => o.id === id)) items.push({ id, name: r.org });
	}
	return items;
}
