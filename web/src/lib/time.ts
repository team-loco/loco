import type { Timestamp } from "@bufbuild/protobuf/wkt";

export { timestampFromMs as msToTimestamp } from "@bufbuild/protobuf/wkt";

export const DAY_MS = 86_400_000;

export function maybeTsMs(ts: Timestamp | undefined): number | undefined {
	if (ts === undefined) return undefined;
	return Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000);
}

export function tsMs(ts: Timestamp | undefined): number {
	return maybeTsMs(ts) ?? 0;
}

function pad(n: number, width = 2): string {
	return n.toString().padStart(width, "0");
}

export interface TimePreferences {
	zone: "local" | "utc";
	clock: "24" | "12";
}

function parts(ms: number, pref: TimePreferences) {
	const instant = new Date(ms);
	const d = pref.zone === "utc" ? instant : new Date(ms - instant.getTimezoneOffset() * 60_000);
	return {
		year: d.getUTCFullYear(),
		month: d.getUTCMonth(),
		day: d.getUTCDate(),
		hour: d.getUTCHours(),
		minute: d.getUTCMinutes(),
		second: d.getUTCSeconds(),
		ms: d.getUTCMilliseconds(),
	};
}

function clock(ms: number, seconds: boolean, withMs: boolean, pref: TimePreferences): string {
	const p = parts(ms, pref);
	const h12 = pref.clock === "12";
	const hour = h12 ? String(p.hour % 12 || 12) : pad(p.hour);
	return `${hour}:${pad(p.minute)}${seconds ? `:${pad(p.second)}` : ""}${withMs ? `.${pad(p.ms, 3)}` : ""}${h12 ? (p.hour < 12 ? " AM" : " PM") : ""}`;
}

export function formatHourMinute(ms: number, pref: TimePreferences): string {
	return clock(ms, false, false, pref);
}

export function formatClock(ms: number, withMs: boolean, pref: TimePreferences): string {
	return clock(ms, true, withMs, pref);
}

export function formatSlashDate(ms: number, pref: TimePreferences): string {
	const p = parts(ms, pref);
	return `${String(p.month + 1)}/${pad(p.day)}`;
}

export function formatIsoDate(ms: number, pref: TimePreferences): string {
	const p = parts(ms, pref);
	return `${String(p.year)}-${pad(p.month + 1)}-${pad(p.day)}`;
}

function createDateFormats(timeZone: string) {
	return {
		monthDay: new Intl.DateTimeFormat("en-US", { timeZone, month: "short", day: "numeric" }),
		monthDayYear: new Intl.DateTimeFormat("en-US", { timeZone, month: "short", day: "numeric", year: "numeric" }),
		weekdayDate: new Intl.DateTimeFormat("en-US", { timeZone, weekday: "short", month: "short", day: "numeric" }),
	};
}

const localZoneFormat = new Intl.DateTimeFormat("en-US", { timeZoneName: "short" });
const localZone = localZoneFormat.resolvedOptions().timeZone;
const dateFormats = { local: createDateFormats(localZone), utc: createDateFormats("UTC") };

export function formatMonthDay(ms: number, pref: TimePreferences): string {
	return dateFormats[pref.zone].monthDay.format(ms);
}

export function formatStarted(ms: number | undefined, pref: TimePreferences): string {
	return ms === undefined || ms === 0 ? "—" : `${formatMonthDay(ms, pref)}, ${formatHourMinute(ms, pref)}`;
}

export function formatMonthDayYear(ms: number, pref: TimePreferences): string {
	return dateFormats[pref.zone].monthDayYear.format(ms);
}

export function timeZoneLabel(ms: number, pref: TimePreferences): string {
	if (pref.zone === "utc") return "UTC";
	return localZoneFormat.formatToParts(ms).find((p) => p.type === "timeZoneName")?.value ?? localZone;
}

export function formatFullTime(ms: number, withMs: boolean, pref: TimePreferences): string {
	return `${formatMonthDayYear(ms, pref)}, ${formatClock(ms, withMs, pref)} ${timeZoneLabel(ms, pref)}`;
}

const AXIS_MINUTES = [1, 2, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880, 10080];

export function timeAxis(from: number, to: number, pref: TimePreferences): number[] {
	if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) return [];
	const step = AXIS_MINUTES.find((m) => (to - from) / (m * 60_000) <= 8) ?? 10080;
	const ticks: number[] = [];
	if (step < 1440) {
		for (let t = Math.ceil(from / 60_000) * 60_000; t <= to; t += 60_000) {
			const p = parts(t, pref);
			if ((p.hour * 60 + p.minute) % step === 0) ticks.push(t);
		}
	} else {
		const p = parts(from, pref);
		const days = step / 1440;
		const day = Math.ceil(Date.UTC(p.year, p.month, p.day) / (DAY_MS * days)) * days;
		const d = new Date(day * DAY_MS);
		const local = pref.zone === "utc" ? d : new Date(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
		while (local.getTime() <= to) {
			if (local.getTime() >= from) ticks.push(local.getTime());
			if (pref.zone === "utc") local.setUTCDate(local.getUTCDate() + days);
			else {
				local.setDate(local.getDate() + days);
				local.setHours(0, 0, 0, 0);
			}
		}
	}
	return ticks;
}

export function formatAxisTime(ms: number, from: number, to: number, pref: TimePreferences): string {
	const p = parts(ms, pref);
	return to - from > 4 * DAY_MS || (p.hour === 0 && p.minute === 0 && to - from > DAY_MS / 4)
		? formatMonthDay(ms, pref)
		: formatHourMinute(ms, pref);
}

export function agoLabel(ms: number, nowMs: number): string {
	if (ms === 0) return "—";
	const mins = Math.floor((nowMs - ms) / 60_000);
	if (mins < 1) return "just now";
	if (mins < 60) return `${mins.toString()} min ago`;
	const hrs = Math.floor(mins / 60);
	if (hrs < 24) return `${hrs.toString()} hour${hrs === 1 ? "" : "s"} ago`;
	const days = Math.floor(hrs / 24);
	if (days === 1) return "yesterday";
	return `${days.toString()} days ago`;
}

/** A relative time short enough for a narrow column: "5m ago", "3h ago", "2d ago". */
export function shortAgoLabel(ms: number, nowMs: number): string {
	const mins = Math.max(0, Math.round((nowMs - ms) / 60_000));
	if (mins < 1) return "now";
	if (mins < 60) return `${mins.toString()}m ago`;
	const hrs = Math.round(mins / 60);
	if (hrs < 24) return `${hrs.toString()}h ago`;
	return `${Math.round(hrs / 24).toString()}d ago`;
}

export function parseClockInput(value: string, pref: TimePreferences): string | null {
	const match = /^(\d{1,2}):(\d{2})(?:\s*(AM|PM))?$/i.exec(value.trim());
	if (match === null) return null;
	let hour = Number(match[1]);
	const minute = Number(match[2]);
	if (minute > 59) return null;
	if (pref.clock === "12") {
		if (hour < 1 || hour > 12 || match[3] === undefined) return null;
		hour = (hour % 12) + (match[3].toUpperCase() === "PM" ? 12 : 0);
	} else if (hour > 23 || match[3] !== undefined) return null;
	return `${pad(hour)}:${pad(minute)}`;
}

export function formatWeekdayDate(ms: number, pref: TimePreferences): string {
	return dateFormats[pref.zone].weekdayDate.format(ms);
}

export function previousDay(ms: number, pref: TimePreferences): number {
	const d = new Date(ms);
	if (pref.zone === "utc") d.setUTCDate(d.getUTCDate() - 1);
	else d.setDate(d.getDate() - 1);
	return d.getTime();
}
