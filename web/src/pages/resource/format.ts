import { formatFullTime as fullTime, formatHourMinute, formatMonthDay, timePreferences } from "@/lib/time";

export function shortId(id: string): string {
	return id.slice(0, 8);
}

export function formatStarted(ms: number | undefined, pref = timePreferences()): string {
	return ms === undefined ? "—" : `${formatMonthDay(ms, pref)}, ${formatHourMinute(ms, pref)}`;
}

export function formatFullTime(ms: number, withMs = false, pref = timePreferences()): string {
	return fullTime(ms, withMs, pref);
}

export function formatDuration(ms: number): string {
	const mins = Math.max(0, Math.floor(ms / 60_000));
	const hours = Math.floor(mins / 60);
	const days = Math.floor(hours / 24);
	if (days > 0) {
		const h = hours % 24;
		return h > 0 ? `${days.toString()}d ${h.toString()}h` : `${days.toString()}d`;
	}
	if (hours > 0) {
		const m = mins % 60;
		return m > 0 ? `${hours.toString()}h ${m.toString()}m` : `${hours.toString()}h`;
	}
	return `${Math.max(1, mins).toString()}m`;
}
