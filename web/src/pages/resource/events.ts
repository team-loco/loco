import type { RuntimeEvent as Event } from "@/lib/runtime-events";

import type { Glyph } from "@/components/design/StatusGlyph";
import { maybeTsMs, tsMs } from "@/lib/time";
import { eventSummary, eventTitle, severityOf, severityStyle } from "@/pages/observability/events";

const WARNING_WINDOW_MS = 3_600_000;

const WARNING = "resource.degraded";
/** Problem reasons of a replica that restarts, owned by k8sapi/v1alpha1/problems.go. */
const RESTARTS: ReadonlySet<string> = new Set(["restarting", "restarts_exhausted"]);

export function isWarning(event: Event): boolean {
	return event.type === WARNING;
}

export function newestFirst(events: Event[]): Event[] {
	return events.toSorted((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));
}

export function recentWarningCount(events: Event[], nowMs: number): number {
	return events.filter((e) => {
		const ms = maybeTsMs(e.timestamp);
		return isWarning(e) && ms !== undefined && ms <= nowMs && nowMs - ms <= WARNING_WINDOW_MS;
	}).length;
}

export function latestRestartWarning(events: Event[], region?: string): Event | undefined {
	const latest = new Map<string, Event>();
	for (const event of newestFirst(events)) {
		if ((region === undefined || event.region === region) && !latest.has(event.placementId))
			latest.set(event.placementId, event);
	}
	return [...latest.values()].find((event) => event.type === WARNING && RESTARTS.has(event.reason));
}

/** Deploy outcomes the events drawer lists beside runtime events. */
export const DEPLOY_EVENT_TYPES = ["deployment.ready", "deployment.failed", "deployment.crashed"];

export interface EventRow {
	event: Event;
	title: string;
	detail: string;
	glyph: Glyph;
	warning: boolean;
	count: number;
}

function eventRow(event: Event): EventRow {
	const severity = severityOf(event.type);
	const deploy = DEPLOY_EVENT_TYPES.includes(event.type);
	const title = deploy
		? eventTitle(event.type, event.region)
		: event.reason === ""
			? "Service recovered"
			: event.reason.replaceAll("_", " ");
	const detail = deploy ? eventSummary(event) : event.message;
	return { event, title, detail, glyph: severityStyle(severity).glyph, warning: severity !== "normal", count: 1 };
}

/** Newest first, with each run of identical consecutive events collapsed into its newest row and a count. */
export function eventRows(events: Event[]): EventRow[] {
	const rows: EventRow[] = [];
	for (const event of newestFirst(events)) {
		const row = eventRow(event);
		const last = rows.at(-1);
		if (
			last?.event.type === event.type &&
			last.event.region === event.region &&
			last.title === row.title &&
			last.detail === row.detail
		)
			last.count += 1;
		else rows.push(row);
	}
	return rows;
}
