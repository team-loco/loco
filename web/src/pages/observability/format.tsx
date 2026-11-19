import type { ReactNode } from "react";

import { formatClock, formatSlashDate, type TimePreferences } from "@/lib/time";

import type { Level } from "./query";

export function fmtTs(ms: number, withMs: boolean, pref: TimePreferences): string {
	return `${formatSlashDate(ms, pref)} ${formatClock(ms, withMs, pref)}`;
}

export const LEVEL_LABEL: Record<Level, string> = {
	error: "Error",
	warn: "Warning",
	info: "Info",
	debug: "Debug",
};

export interface LevelStyle {
	badge: string;
	body: string;
	dot: string;
	text: string;
}

export function levelStyle(level: Level): LevelStyle {
	switch (level) {
		case "error":
			return {
				badge: "bg-bad-bg text-bad-fg",
				body: "text-bad-fg",
				dot: "bg-err",
				text: "text-bad-fg",
			};
		case "warn":
			return {
				badge: "bg-warn-bg text-warn-fg",
				body: "text-foreground",
				dot: "bg-lv-warn",
				text: "text-warn-fg",
			};
		case "info":
			return {
				badge: "bg-panel2 text-fg2",
				body: "text-foreground",
				dot: "bg-lv-info",
				text: "text-fg3",
			};
		case "debug":
			return {
				badge: "bg-transparent text-fg3",
				body: "text-fg3",
				dot: "bg-lv-debug",
				text: "text-fg3",
			};
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

let wordsRe: { key: string; re: RegExp } | null = null;

function wordsRegExp(words: string[]): RegExp {
	const key = words.join("\n");
	if (wordsRe?.key !== key) wordsRe = { key, re: new RegExp(`(${words.map(escapeRe).join("|")})`, "gi") };
	return wordsRe.re;
}

function highlight(s: string, words: string[], keyBase: string): ReactNode[] {
	if (words.length === 0) return [s];
	return s.split(wordsRegExp(words)).map((p, i) =>
		i % 2 === 1 ? (
			<mark key={`${keyBase}-h${String(i)}`} className="rounded-[2px] bg-[rgba(234,179,8,0.35)] px-px text-inherit">
				{p}
			</mark>
		) : (
			p
		),
	);
}

// oxlint-disable-next-line no-control-regex
const SGR_RE = /(?:\u001b\[|\u009b)[\d:;]*m/g;

/** A log body as plain text, with ANSI colour codes removed and search words marked. */
export function bodyParts(source: string, words: string[]): ReactNode[] {
	return highlight(source.replace(SGR_RE, ""), words, "t");
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
			className="mr-2 inline-block rounded-[3px] bg-panel2 px-1 align-[1px] text-[10px] leading-[15px] font-semibold text-fg3"
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
