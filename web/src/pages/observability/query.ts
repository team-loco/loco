import type { ParsedQuery } from "@/lib/obs-query-parser";
import type { ClusterTransport } from "@/lib/obs";

import type { ObsResource } from "./context";

export type Level = "error" | "warn" | "info" | "debug";

export const LEVELS: Level[] = ["error", "warn", "info", "debug"];

const LEVEL_VARIANTS: Record<Level, string[]> = {
	error: ["ERROR", "error", "Error", "FATAL", "fatal", "Fatal", "CRITICAL", "critical", "Critical"],
	warn: ["WARN", "warn", "Warn", "WARNING", "warning", "Warning"],
	info: ["INFO", "info", "Info", "Information", "NOTICE", "notice"],
	debug: ["DEBUG", "debug", "Debug", "TRACE", "trace", "Trace"],
};

export function levelOf(severity: string): Level {
	const s = severity.toLowerCase();
	if (s.startsWith("err") || s.startsWith("fatal") || s.startsWith("crit") || s.startsWith("emerg") || s.startsWith("alert")) {
		return "error";
	}
	if (s.startsWith("warn")) return "warn";
	if (s.startsWith("debug") || s.startsWith("trace")) return "debug";
	return "info";
}

export type FieldKey = "level" | "resource" | "replica" | "region" | "status" | "method" | "path" | "trace_id";

export interface FieldDef {
	key: FieldKey;
	info: string;
	supported: boolean;
}

export const FIELDS: FieldDef[] = [
	{ key: "level", info: "error · warn · info · debug", supported: true },
	{ key: "resource", info: "service name", supported: true },
	{ key: "replica", info: "replica (pod) name", supported: true },
	{ key: "region", info: "cluster region", supported: true },
	{ key: "status", info: "HTTP status", supported: false },
	{ key: "method", info: "HTTP method", supported: false },
	{ key: "path", info: "request path", supported: false },
	{ key: "trace_id", info: "trace id", supported: false },
];

const FIELD_KEYS = new Set<string>(FIELDS.map((f) => f.key));

export function isFieldKey(key: string): key is FieldKey {
	return FIELD_KEYS.has(key);
}

export function fieldDef(key: FieldKey): FieldDef | undefined {
	return FIELDS.find((f) => f.key === key);
}

export interface Token {
	neg: boolean;
	key: FieldKey;
	value: string;
}

const TOKEN_RE = /(-?)@(\w+):(\S+)/g;

export function tokStr(t: Token): string {
	return `${t.neg ? "-" : ""}@${t.key}:${t.value}`;
}

export function queryStr(tokens: Token[], text: string): string {
	return [...tokens.map(tokStr), text.trim()].filter(Boolean).join(" ");
}

export function parseQuery(q: string): { tokens: Token[]; text: string } {
	const tokens: Token[] = [];
	for (const m of q.matchAll(TOKEN_RE)) {
		const key = m[2] ?? "";
		if (isFieldKey(key)) tokens.push({ neg: m[1] === "-", key, value: m[3] ?? "" });
	}
	const text = q
		.replace(TOKEN_RE, (all: string, _neg: string, key: string) => (isFieldKey(key) ? "" : all))
		.replace(/\s+/g, " ")
		.trim();
	return { tokens, text };
}

export function sameToken(a: Token, b: Token): boolean {
	return a.key === b.key && a.neg === b.neg && a.value.toLowerCase() === b.value.toLowerCase();
}

export function tokenSupport(tokens: Token[]): boolean[] {
	let replicaSeen = false;
	return tokens.map((t) => {
		const def = fieldDef(t.key);
		if (def?.supported !== true) return false;
		if (t.key === "replica") {
			if (t.neg || replicaSeen) return false;
			replicaSeen = true;
		}
		return true;
	});
}

function matches(value: string, pattern: string): boolean {
	const v = value.toLowerCase();
	const p = pattern.toLowerCase();
	return p.endsWith("*") ? v.startsWith(p.slice(0, -1)) : v === p;
}

function narrow<T extends string>(universe: T[], tokens: Token[]): T[] | null {
	if (tokens.length === 0) return null;
	const pos = tokens.filter((t) => !t.neg);
	const neg = tokens.filter((t) => t.neg);
	return universe.filter(
		(u) => (pos.length === 0 || pos.some((t) => matches(u, t.value))) && !neg.some((t) => matches(u, t.value)),
	);
}

export interface BackendQuery {
	parsed: ParsedQuery;
	resourceIds: string[];
	transports: ClusterTransport[];
	impossible: boolean;
}

export function buildBackendQuery(
	tokens: Token[],
	text: string,
	resources: ObsResource[],
	transports: ClusterTransport[],
): BackendQuery {
	const support = tokenSupport(tokens);
	const live = tokens.filter((_, i) => support[i] === true);
	const byKey = (k: FieldKey) => live.filter((t) => t.key === k);

	const levelSet = narrow(LEVELS, byKey("level"));
	const levels = levelSet === null ? [] : levelSet.flatMap((l) => LEVEL_VARIANTS[l]);

	const resourceNames = resources.map((r) => r.name);
	const resSet = narrow(resourceNames, byKey("resource"));
	const resourceIds =
		resSet === null ? [] : resources.filter((r) => resSet.includes(r.name)).map((r) => r.id);

	const regions = [...new Set(transports.map((t) => t.cluster.region))];
	const regionSet = narrow(regions, byKey("region"));
	const liveTransports =
		regionSet === null ? transports : transports.filter((t) => regionSet.includes(t.cluster.region));

	const labels: Record<string, string> = {};
	const replica = byKey("replica")[0];
	if (replica !== undefined) labels["k8s.pod.name"] = replica.value;

	const impossible =
		levelSet?.length === 0 || resSet?.length === 0 || liveTransports.length === 0;

	return {
		parsed: { search: text.trim(), levels, labels },
		resourceIds,
		transports: liveTransports,
		impossible,
	};
}

const SAVE_KEY = "loco_log_searches";

export interface SavedSearches {
	recent: string[];
	pinned: string[];
}

function isStringArray(v: unknown): v is string[] {
	return Array.isArray(v) && v.every((x) => typeof x === "string");
}

export function readSaved(): SavedSearches {
	try {
		const raw: unknown = JSON.parse(localStorage.getItem(SAVE_KEY) ?? "null");
		if (raw !== null && typeof raw === "object" && "recent" in raw && "pinned" in raw) {
			const { recent, pinned } = raw;
			if (isStringArray(recent) && isStringArray(pinned)) return { recent, pinned };
		}
	} catch {
		return { recent: [], pinned: [] };
	}
	return { recent: [], pinned: [] };
}

export function writeSaved(saved: SavedSearches) {
	try {
		localStorage.setItem(SAVE_KEY, JSON.stringify(saved));
	} catch {
		return;
	}
}
