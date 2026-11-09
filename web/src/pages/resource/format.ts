import { formatClock, formatIsoDate } from "@/lib/time";

export function shortId(id: string): string {
	return id.slice(0, 8);
}

const startedFormat = new Intl.DateTimeFormat("en-US", {
	month: "short",
	day: "numeric",
	hour: "2-digit",
	minute: "2-digit",
	hourCycle: "h23",
});

export function formatStarted(ms: number | undefined): string {
	if (ms === undefined) return "—";
	return startedFormat.format(ms);
}

export function formatFullTime(ms: number): string {
	return `${formatIsoDate(ms)} ${formatClock(ms)}`;
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
