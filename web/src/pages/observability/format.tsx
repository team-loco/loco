import type { ReactNode } from "react";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

import { cn } from "@/lib/utils";

import type { Level } from "./query";

const pad = (n: number, w = 2) => String(n).padStart(w, "0");

export function tsMs(ts: Timestamp | undefined): number {
	if (!ts) return 0;
	return Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6);
}

export function fmtTs(ms: number, withMs = false): string {
	const d = new Date(ms);
	const base = `${String(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
	return withMs ? `${base}.${pad(d.getMilliseconds(), 3)}` : base;
}

export function fmtClock(ms: number, withMs = false): string {
	const full = fmtTs(ms, withMs);
	return full.split(" ").pop() ?? full;
}

export interface LevelStyle {
	bar: string;
	badge: string;
	body: string;
	dot: string;
}

export function levelStyle(level: Level): LevelStyle {
	switch (level) {
		case "error":
			return { bar: "border-l-err", badge: "bg-bad-bg text-bad-fg", body: "text-bad-fg", dot: "bg-err" };
		case "warn":
			return { bar: "border-l-warn", badge: "bg-warn-bg text-warn-fg", body: "text-foreground", dot: "bg-warn" };
		case "info":
			return { bar: "border-l-transparent", badge: "bg-bg3 text-fg2", body: "text-foreground", dot: "bg-info" };
		case "debug":
			return { bar: "border-l-transparent", badge: "bg-transparent text-fg4", body: "text-fg3", dot: "bg-fg4" };
	}
}

export type JsonObject = Record<string, unknown>;

function isJsonObject(v: unknown): v is JsonObject {
	return v !== null && typeof v === "object" && !Array.isArray(v);
}

export function parseJsonMsg(s: string): JsonObject | null {
	const t = s.trim();
	if (!/^[[{]/.test(t)) return null;
	try {
		const v: unknown = JSON.parse(t);
		return isJsonObject(v) ? v : null;
	} catch {
		return null;
	}
}

function escapeRe(w: string): string {
	return w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function highlight(s: string, words: string[], keyBase: string): ReactNode[] {
	if (words.length === 0) return [s];
	const re = new RegExp(`(${words.map(escapeRe).join("|")})`, "gi");
	return s.split(re).map((p, i) =>
		i % 2 === 1 ? (
			<mark key={`${keyBase}-h${String(i)}`} className="rounded-[2px] bg-[rgba(234,179,8,0.35)] px-px text-inherit">
				{p}
			</mark>
		) : (
			p
		),
	);
}

const BODY_RE = /(\b[1-5]\d\d\b)|(\b\d+(?:\.\d+)?(?:ms|s|MB|MiB)\b)|(\b[\w.]+=[^\s]+)|("[^"]*")/g;

function statusClass(code: string): string {
	if (code.startsWith("5")) return "text-err";
	if (code.startsWith("4")) return "text-warn";
	if (code.startsWith("2")) return "text-ok-fg";
	return "text-fg3";
}

export function bodyParts(body: string, words: string[]): ReactNode[] {
	const out: ReactNode[] = [];
	let last = 0;
	let k = 0;
	for (const m of body.matchAll(BODY_RE)) {
		const idx = m.index;
		if (idx > last) out.push(...highlight(body.slice(last, idx), words, `t${String(k++)}`));
		const [, status, dur, kv, quoted] = m;
		if (status !== undefined) {
			out.push(
				<span key={`s${String(k++)}`} className={cn("font-semibold", statusClass(status))}>
					{status}
				</span>,
			);
		} else if (dur !== undefined) {
			out.push(
				<span key={`d${String(k++)}`} className="text-fg3">
					{dur}
				</span>,
			);
		} else if (kv !== undefined) {
			const eq = kv.indexOf("=");
			const key = `k${String(k++)}`;
			out.push(
				<span key={key}>
					<span className="text-fg3">{kv.slice(0, eq + 1)}</span>
					<span className="text-info-fg">{highlight(kv.slice(eq + 1), words, key)}</span>
				</span>,
			);
		} else if (quoted !== undefined) {
			out.push(
				<span key={`q${String(k++)}`} className="text-ok-fg">
					{quoted}
				</span>,
			);
		}
		last = idx + m[0].length;
	}
	if (last < body.length) out.push(...highlight(body.slice(last), words, `t${String(k++)}`));
	return out;
}

const MAIN_KEYS = ["msg", "message", "event"];

function scalarString(v: unknown): string {
	if (typeof v === "string") return v;
	if (typeof v === "number" || typeof v === "boolean" || typeof v === "bigint") return String(v);
	if (v === null || v === undefined) return "null";
	return JSON.stringify(v);
}

export function jsonPreview(obj: JsonObject, words: string[]): ReactNode[] {
	const mainKey = MAIN_KEYS.find((k) => k in obj);
	const main = mainKey !== undefined ? obj[mainKey] : undefined;
	const out: ReactNode[] = [
		<span
			key="tag"
			className="mr-2 inline-block rounded-[3px] bg-bg3 px-1 align-[1px] text-[10px] leading-[15px] font-semibold text-fg3"
		>
			JSON
		</span>,
	];
	if (main !== undefined) {
		out.push(
			<span key="m" className="mr-2.5">
				{bodyParts(scalarString(main), words)}
			</span>,
		);
	}
	Object.entries(obj)
		.filter(([k]) => !MAIN_KEYS.includes(k))
		.forEach(([k, v], i) => {
			const isObj = v !== null && typeof v === "object";
			const vs = isObj ? "{…}" : scalarString(v);
			out.push(
				<span key={`r${String(i)}`} className="mr-2.5">
					<span className="text-fg3">{k}=</span>
					<span className={typeof v === "number" ? "text-info-fg" : isObj ? "text-fg4" : "text-fg2"}>{vs}</span>
				</span>,
			);
		});
	return out;
}

export function flattenJson(obj: JsonObject, prefix = ""): Record<string, string> {
	const out: Record<string, string> = {};
	for (const [k, v] of Object.entries(obj)) {
		const key = prefix === "" ? k : `${prefix}.${k}`;
		if (isJsonObject(v)) {
			Object.assign(out, flattenJson(v, key));
		} else {
			out[`body.${key}`] = Array.isArray(v) ? JSON.stringify(v) : scalarString(v);
		}
	}
	return out;
}
