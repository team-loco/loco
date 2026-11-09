import type { Event } from "@gen/loco/resource/v1/resource_pb";

import { maybeTsMs, tsMs } from "@/lib/time";

const WARNING_WINDOW_MS = 3_600_000;

const WARNING = "Warning";
const BACK_OFF = "BackOff";

export function isWarning(event: Event): boolean {
	return event.type === WARNING;
}

export function newestFirst(events: Event[]): Event[] {
	return events.toSorted((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));
}

export function recentWarningCount(events: Event[], nowMs: number): number {
	return events.filter((e) => {
		const ms = maybeTsMs(e.timestamp);
		return isWarning(e) && ms !== undefined && nowMs - ms <= WARNING_WINDOW_MS;
	}).length;
}

export function latestBackOff(events: Event[]): Event | undefined {
	return newestFirst(events).find((e) => e.reason === BACK_OFF && e.podName !== "");
}
