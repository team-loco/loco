import { create } from "@bufbuild/protobuf";
import { TimestampSchema, type Timestamp } from "@bufbuild/protobuf/wkt";

export const DAY_MS = 86_400_000;

export function maybeTsMs(ts: Timestamp | undefined): number | undefined {
	if (ts === undefined) return undefined;
	return Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000);
}

export function tsMs(ts: Timestamp | undefined): number {
	return maybeTsMs(ts) ?? 0;
}

export function msToTimestamp(ms: number): Timestamp {
	return create(TimestampSchema, { seconds: BigInt(Math.floor(ms / 1000)), nanos: 0 });
}

function pad(n: number, width = 2): string {
	return n.toString().padStart(width, "0");
}

export function formatHourMinute(ms: number): string {
	const d = new Date(ms);
	return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function formatClock(ms: number, withMs = false): string {
	const d = new Date(ms);
	const base = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
	return withMs ? `${base}.${pad(d.getMilliseconds(), 3)}` : base;
}

export function formatSlashDate(ms: number): string {
	const d = new Date(ms);
	return `${(d.getMonth() + 1).toString()}/${pad(d.getDate())}`;
}

export function formatIsoDate(ms: number): string {
	const d = new Date(ms);
	return `${d.getFullYear().toString()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

const monthDayFormat = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" });

const monthDayYearFormat = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric" });

export function formatMonthDay(ms: number): string {
	return monthDayFormat.format(ms);
}

export function formatMonthDayYear(ms: number): string {
	return monthDayYearFormat.format(ms);
}
