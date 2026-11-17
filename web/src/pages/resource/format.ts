import { formatFullTime as fullTime, type TimePreferences } from "@/lib/time";

export function formatFullTime(ms: number, withMs: boolean, pref: TimePreferences): string {
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
