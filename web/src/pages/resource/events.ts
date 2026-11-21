import { RUNTIME_EVENT_TYPES, type RuntimeEvent as Event } from "@/lib/runtime-events";

import type { Glyph } from "@/components/design/StatusGlyph";
import { maybeTsMs, tsMs } from "@/lib/time";
import { eventSummary, eventTitle, severityOf, severityStyle } from "@/pages/observability/events";

const WARNING_WINDOW_MS = 3_600_000;

const WARNING = "resource.degraded";

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

/** Deploy outcomes the events drawer lists beside runtime events. */
const DEPLOY_EVENT_TYPES = ["deployment.ready", "deployment.failed", "deployment.crashed"];

/** The event types the events drawer asks the API for: runtime events and deploy outcomes, or only the warnings and failures among them. */
export function drawerEventTypes(warningsOnly: boolean): string[] {
	const all = [...RUNTIME_EVENT_TYPES, ...DEPLOY_EVENT_TYPES];
	return warningsOnly ? all.filter((type) => severityOf(type) !== "normal") : all;
}

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
