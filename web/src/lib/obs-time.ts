import { DAY_MS, formatHourMinute, formatMonthDay } from "./time";

export const MINUTE_MS = 60_000;
export const HOUR_MS = 60 * MINUTE_MS;
const WEEK_MS = 7 * DAY_MS;

export const QUERY_MAX_MS = DAY_MS;
export const EVENTS_MAX_MS = WEEK_MS;
export const DEFAULT_SPAN_MS = HOUR_MS;
export const AROUND_MS = 5 * MINUTE_MS;

const PRESETS_MS = [15 * MINUTE_MS, HOUR_MS, 4 * HOUR_MS, 12 * HOUR_MS, DAY_MS, 2 * DAY_MS, WEEK_MS];

const UNITS: { suffix: string; ms: number; name: string }[] = [
	{ suffix: "w", ms: WEEK_MS, name: "week" },
	{ suffix: "d", ms: DAY_MS, name: "day" },
	{ suffix: "h", ms: HOUR_MS, name: "hour" },
	{ suffix: "m", ms: MINUTE_MS, name: "minute" },
];

const SPAN_RE = /^(?:(?:past|last)\s+)?(\d+)\s*([a-z]+)$/;

const STEP_TARGET_POINTS = 60;
const MIN_STEP_SECONDS = 60;

export type TimeWindow = { kind: "relative"; spanMs: number } | { kind: "absolute"; from: number; to: number };

export interface Bounds {
	from: number;
	to: number;
}

export function presetsWithin(maxMs: number): number[] {
	return PRESETS_MS.filter((ms) => ms <= maxMs);
}

function unitFor(word: string) {
	if (word === "mo" || word.startsWith("mon")) return undefined;
	return UNITS.find(
		(u) => word === u.suffix || word.startsWith(u.name) || (u.suffix === "m" && word.startsWith("min")),
	);
}

export function parseSpan(text: string): number | null {
	const match = SPAN_RE.exec(text.trim().toLowerCase());
	if (match === null) return null;
	const count = Number(match[1]);
	const unit = unitFor(match[2] ?? "");
	if (unit === undefined || !Number.isSafeInteger(count) || count <= 0) return null;
	return count * unit.ms;
}

function spanUnit(ms: number): { count: number; suffix: string; name: string } {
	const minutes = Math.max(1, Math.round(ms / MINUTE_MS));
	const rounded = minutes * MINUTE_MS;
	if (rounded === DAY_MS) return { count: 24, suffix: "h", name: "hour" };
	const unit = UNITS.find((u) => rounded % u.ms === 0);
	if (unit === undefined) return { count: minutes, suffix: "m", name: "minute" };
	return { count: rounded / unit.ms, suffix: unit.suffix, name: unit.name };
}

export function formatSpan(ms: number): string {
	const { count, suffix } = spanUnit(ms);
	return `${String(count)}${suffix}`;
}

export function spanLabel(ms: number): string {
	const { count, name } = spanUnit(ms);
	return count === 1 ? `Past ${name}` : `Past ${String(count)} ${name}s`;
}

function readMs(params: URLSearchParams, key: string): number | null {
	const value = Number(params.get(key) ?? "");
	return params.has(key) && Number.isSafeInteger(value) && value > 0 ? value : null;
}

export function readWindow(params: URLSearchParams, maxMs: number): TimeWindow {
	const from = readMs(params, "from");
	const to = readMs(params, "to");
	if (from !== null && to !== null && to > from) return { kind: "absolute", from: Math.max(from, to - maxMs), to };
	const span = parseSpan(params.get("range") ?? "") ?? DEFAULT_SPAN_MS;
	return { kind: "relative", spanMs: Math.min(span, maxMs) };
}

export function writeWindow(params: URLSearchParams, window: TimeWindow): URLSearchParams {
	const next = new URLSearchParams(params);
	if (window.kind === "relative") {
		next.set("range", formatSpan(window.spanMs));
		next.delete("from");
		next.delete("to");
	} else {
		next.delete("range");
		next.set("from", String(Math.round(window.from)));
		next.set("to", String(Math.round(window.to)));
	}
	return next;
}

export function resolveWindow(window: TimeWindow, now: number): Bounds {
	if (window.kind === "absolute") return { from: window.from, to: window.to };
	return { from: now - window.spanMs, to: now };
}

export function windowSpan(window: TimeWindow): number {
	return window.kind === "relative" ? window.spanMs : window.to - window.from;
}

export function shiftWindow(window: TimeWindow, direction: -1 | 1, now: number): TimeWindow {
	const { from, to } = resolveWindow(window, now);
	const span = to - from;
	const nextTo = to + direction * span;
	if (nextTo >= now) return { kind: "relative", spanMs: span };
	return { kind: "absolute", from: from + direction * span, to: nextTo };
}

export function aroundWindow(ts: number, now: number): TimeWindow {
	return { kind: "absolute", from: ts - AROUND_MS, to: Math.min(now, ts + AROUND_MS) };
}

export type CustomRangeError = "invalid" | "order" | "future" | "too-long";

export function checkCustomRange(from: number, to: number, now: number, maxMs: number): CustomRangeError | null {
	if (!Number.isFinite(from) || !Number.isFinite(to)) return "invalid";
	if (from >= to) return "order";
	if (from >= now) return "future";
	if (to - from > maxMs) return "too-long";
	return null;
}

export function customRangeMessage(error: CustomRangeError, maxMs: number): string {
	switch (error) {
		case "invalid":
			return "Enter a start and an end.";
		case "order":
			return "The start has to be before the end.";
		case "future":
			return "The start has to be in the past.";
		case "too-long":
			return `Pick a range of ${formatSpan(maxMs)} or less.`;
	}
}

function sameDay(a: number, b: number): boolean {
	return new Date(a).toDateString() === new Date(b).toDateString();
}

function dayClock(ms: number): string {
	return `${formatMonthDay(ms)} ${formatHourMinute(ms)}`;
}

export function windowLabel(window: TimeWindow): string {
	if (window.kind === "relative") return spanLabel(window.spanMs);
	const end = sameDay(window.from, window.to) ? formatHourMinute(window.to) : dayClock(window.to);
	return `${dayClock(window.from)} – ${end}`;
}

export function stepSeconds(spanMs: number): number {
	const raw = spanMs / 1000 / STEP_TARGET_POINTS;
	return Math.max(MIN_STEP_SECONDS, Math.ceil(raw / MIN_STEP_SECONDS) * MIN_STEP_SECONDS);
}

export function toLocalInput(ms: number): string {
	const d = new Date(ms);
	const pad = (n: number) => String(n).padStart(2, "0");
	return `${String(d.getFullYear())}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function fromLocalInput(value: string): number {
	return value === "" ? Number.NaN : new Date(value).getTime();
}
