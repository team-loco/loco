import type { RuntimeEvent as Event } from "@/lib/runtime-events";

import { maybeTsMs, tsMs } from "@/lib/time";

const WARNING_WINDOW_MS = 3_600_000;

const WARNING = "resource.degraded";
const BACK_OFF = "crash_looping";

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

export function latestBackOff(events: Event[], region?: string): Event | undefined {
	const latest = new Map<string, Event>();
	for (const event of newestFirst(events)) {
		if ((region === undefined || event.region === region) && !latest.has(event.placementId))
			latest.set(event.placementId, event);
	}
	return [...latest.values()].find((event) => event.type === WARNING && event.reason === BACK_OFF);
}
