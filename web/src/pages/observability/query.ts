import type { ParsedQuery } from "@/lib/obs-query-parser";

import type { ObservabilityView } from "@/lib/routes";

import type { ObsResource } from "./context";

export type Level = "error" | "warn" | "info" | "debug";

export const LEVELS: Level[] = ["error", "warn", "info", "debug"];

const LEVEL_VARIANTS: Record<Level, string[]> = {
	error: ["ERROR", "error", "Error", "FATAL", "fatal", "Fatal", "CRITICAL", "critical", "Critical"],
	warn: ["WARN", "warn", "Warn", "WARNING", "warning", "Warning"],
	info: ["", "INFO", "info", "Info", "Information", "NOTICE", "notice"],
	debug: ["DEBUG", "debug", "Debug", "TRACE", "trace", "Trace"],
};

export function levelOf(severity: string): Level {
	const s = severity.toLowerCase();
	if (
		s.startsWith("err") ||
		s.startsWith("fatal") ||
		s.startsWith("crit") ||
		s.startsWith("emerg") ||
		s.startsWith("alert")
	) {
		return "error";
	}
	if (s.startsWith("warn")) return "warn";
	if (s.startsWith("debug") || s.startsWith("trace")) return "debug";
	return "info";
}

export type FieldKey =
	"resource" | "level" | "replica" | "build" | "region" | "type" | "status" | "trace" | "duration" | "method" | "path";

export interface FieldDef {
	key: FieldKey;
	label: string;
	views: ObservabilityView[];
	negate: ObservabilityView[];
	single: boolean;
	planned: ObservabilityView[];
}

const ALL_VIEWS: ObservabilityView[] = ["logs", "metrics", "traces", "events"];

export const FIELDS: FieldDef[] = [
	{ key: "resource", label: "Resource", views: ALL_VIEWS, negate: ALL_VIEWS, single: false, planned: [] },
	{ key: "level", label: "Level", views: ["logs"], negate: ["logs"], single: false, planned: [] },
	{ key: "replica", label: "Replica", views: ["logs"], negate: [], single: true, planned: [] },
	{ key: "build", label: "Build ID", views: ["logs"], negate: [], single: true, planned: [] },
	{
		key: "region",
		label: "Region",
		views: ["logs", "metrics"],
		negate: ["logs", "metrics"],
		single: false,
		planned: [],
	},
	{ key: "type", label: "Event type", views: ["events"], negate: ["events"], single: false, planned: [] },
	{ key: "status", label: "Trace status", views: ["traces"], negate: ["traces"], single: false, planned: ["logs"] },
	{ key: "trace", label: "Trace ID", views: ["traces"], negate: [], single: true, planned: ["logs"] },
	{ key: "duration", label: "Duration (ms)", views: ["traces"], negate: [], single: false, planned: [] },
	{ key: "method", label: "HTTP method", views: [], negate: [], single: false, planned: ["logs"] },
	{ key: "path", label: "Request path", views: [], negate: [], single: false, planned: ["logs"] },
];

const KEY_ALIASES: Record<string, FieldKey> = { pod: "replica", trace_id: "trace" };

function fieldKey(key: string): FieldKey | undefined {
	return FIELDS.find((f) => f.key === key)?.key ?? KEY_ALIASES[key];
}

export function isFieldKey(key: string): key is FieldKey {
	return FIELDS.some((f) => f.key === key);
}

export function fieldDef(key: FieldKey): FieldDef {
	const def = FIELDS.find((f) => f.key === key);
	if (def === undefined) throw new Error(`unknown field ${key}`);
	return def;
}

export function viewFields(view: ObservabilityView): FieldDef[] {
	return FIELDS.filter((f) => f.views.includes(view) || f.planned.includes(view));
}

export function searchesText(view: ObservabilityView): boolean {
	return view !== "metrics";
}

export interface Token {
	neg: boolean;
	key: FieldKey;
	value: string;
}

const TOKEN_RE = /(-?)@(\w+):(\S+)/g;
const BARE_TOKEN_RE = /^(pod|replica|build):(\S+)$/;

export const BUILD_ID_LABEL = "loco.io/build-id";

function tokStr(t: Token): string {
	return `${t.neg ? "-" : ""}@${t.key}:${t.value}`;
}

export function queryStr(tokens: Token[], text: string): string {
	return [...tokens.map(tokStr), text.trim()].filter(Boolean).join(" ");
}

export function parseQuery(q: string): { tokens: Token[]; text: string } {
	const bare = BARE_TOKEN_RE.exec(q.trim());
	const bareKey = fieldKey(bare?.[1] ?? "");
	if (bareKey !== undefined) return { tokens: [{ neg: false, key: bareKey, value: bare?.[2] ?? "" }], text: "" };
	const tokens: Token[] = [];
	for (const m of q.matchAll(TOKEN_RE)) {
		const key = fieldKey(m[2] ?? "");
		if (key !== undefined) tokens.push({ neg: m[1] === "-", key, value: m[3] ?? "" });
	}
	const text = q
		.replace(TOKEN_RE, (all: string, _neg: string, key: string) => (fieldKey(key) !== undefined ? "" : all))
		.replace(/\s+/g, " ")
		.trim();
	return { tokens, text };
}

export function readQuery(params: URLSearchParams): { tokens: Token[]; text: string } {
	const parsed = parseQuery(params.get("q") ?? "");
	const named = (params.get("r") ?? "")
		.split(",")
		.filter(Boolean)
		.map((value): Token => ({ neg: false, key: "resource", value }));
	return { tokens: [...named, ...parsed.tokens], text: parsed.text };
}

export function writeQuery(params: URLSearchParams, tokens: Token[], text: string): URLSearchParams {
	const next = new URLSearchParams(params);
	next.delete("r");
	const q = queryStr(tokens, text);
	if (q === "") next.delete("q");
	else next.set("q", q);
	return next;
}

export function sameToken(a: Token, b: Token): boolean {
	return a.key === b.key && a.neg === b.neg && a.value.toLowerCase() === b.value.toLowerCase();
}

export interface TokenSupport {
	ok: boolean;
	reason: string;
}

const SUPPORTED: TokenSupport = { ok: true, reason: "" };

export function tokenSupport(view: ObservabilityView, tokens: Token[]): TokenSupport[] {
	const seen = new Set<FieldKey>();
	return tokens.map((t) => {
		const def = fieldDef(t.key);
		if (!def.views.includes(view)) {
			return {
				ok: false,
				reason: def.planned.includes(view)
					? `Filtering ${view} by @${t.key} isn't supported yet`
					: `@${t.key} doesn't apply to ${view}`,
			};
		}
		if (t.neg && !def.negate.includes(view)) return { ok: false, reason: `Excluding @${t.key} isn't supported yet` };
		if (def.single && seen.has(t.key)) return { ok: false, reason: `Only the first @${t.key} filter applies` };
		seen.add(t.key);
		return SUPPORTED;
	});
}

export function liveTokens(view: ObservabilityView, tokens: Token[], key: FieldKey): Token[] {
	const support = tokenSupport(view, tokens);
	return tokens.filter((t, i) => t.key === key && support[i]?.ok === true);
}

function matches(value: string, pattern: string): boolean {
	const v = value.toLowerCase();
	const p = pattern.toLowerCase();
	return p.endsWith("*") ? v.startsWith(p.slice(0, -1)) : v === p;
}

export function narrow<T extends string>(universe: T[], tokens: Token[]): T[] | null {
	return tokens.length === 0 ? null : universe.filter((u) => passes([u], tokens));
}

export function passes(values: string[], tokens: Token[]): boolean {
	const pos = tokens.filter((t) => !t.neg);
	const neg = tokens.filter((t) => t.neg);
	return (
		(pos.length === 0 || pos.some((t) => values.some((v) => matches(v, t.value)))) &&
		!neg.some((t) => values.some((v) => matches(v, t.value)))
	);
}

const DURATION_RE = /^(>=?|<=?)(\d+(?:\.\d+)?)(us|µs|ms|s)?$/;
const NANOS_PER_UNIT: Record<string, number> = { us: 1e3, µs: 1e3, ms: 1e6, s: 1e9 };

export interface DurationBounds {
	minNanos?: bigint;
	maxNanos?: bigint;
}

/** Reads @duration:>500 style tokens (milliseconds unless a unit is given) into bounds, or null when one is malformed. */
export function durationBounds(tokens: Token[]): DurationBounds | null {
	const bounds: DurationBounds = {};
	for (const t of liveTokens("traces", tokens, "duration")) {
		const m = DURATION_RE.exec(t.value.toLowerCase());
		if (m === null) return null;
		const nanos = BigInt(Math.round(Number(m[2]) * (NANOS_PER_UNIT[m[3] ?? "ms"] ?? 0)));
		if (m[1]?.startsWith(">") === true) {
			if (bounds.minNanos === undefined || nanos > bounds.minNanos) bounds.minNanos = nanos;
		} else if (bounds.maxNanos === undefined || nanos < bounds.maxNanos) {
			bounds.maxNanos = nanos;
		}
	}
	return bounds;
}

export interface BackendQuery {
	parsed: ParsedQuery;
	resourceIds: string[];
	allEnvironments: boolean;
	impossible: boolean;
}

export function buildBackendQuery(
	tokens: Token[],
	text: string,
	resources: ObsResource[],
	scopedResourceIds: string[] | null,
): BackendQuery {
	const byKey = (k: FieldKey) => liveTokens("logs", tokens, k);

	const levelSet = narrow(LEVELS, byKey("level"));
	const levels = levelSet === null ? [] : levelSet.flatMap((l) => LEVEL_VARIANTS[l]);

	const resourceNames = resources.map((r) => r.name);
	const resNames = narrow(resourceNames, byKey("resource"));
	const resSet = resNames === null ? null : new Set(resNames);
	const resourceIds =
		resSet === null ? (scopedResourceIds ?? []) : resources.filter((r) => resSet.has(r.name)).map((r) => r.id);

	const labels: Record<string, string> = {};
	const replica = byKey("replica")[0];
	if (replica !== undefined) labels["k8s.pod.name"] = replica.value;
	const build = byKey("build")[0];
	if (build !== undefined) labels[BUILD_ID_LABEL] = build.value;

	const impossible =
		levelSet?.length === 0 || resNames?.length === 0 || (scopedResourceIds !== null && resourceIds.length === 0);

	return {
		parsed: {
			search: text.trim(),
			levels,
			labels,
			regions: byKey("region")
				.filter((t) => !t.neg)
				.map((t) => t.value),
			excludedRegions: byKey("region")
				.filter((t) => t.neg)
				.map((t) => t.value),
		},
		resourceIds,
		allEnvironments: build !== undefined,
		impossible,
	};
}

export const PINNED_KEY = "loco:pinned-queries:v1";
const RECENT_KEY_PREFIX = "loco:recent-queries:v1";
const RECENT_VIEWS = new Set<ObservabilityView>(["logs", "metrics", "traces"]);
const RECENT_KEEP = 8;
const RECENT_SHOWN = 5;
const MINUTE_MS = 60_000;
const HOUR_MINUTES = 60;
const DAY_MINUTES = 1440;

export interface RecentQuery {
	q: string;
	ts: number;
}

export function pinnedQueries(v: unknown): string[] {
	return Array.isArray(v) && v.every((x) => typeof x === "string") ? v : [];
}

function isRecentQuery(x: unknown): x is RecentQuery {
	return (
		typeof x === "object" && x !== null && "q" in x && "ts" in x && typeof x.q === "string" && typeof x.ts === "number"
	);
}

export function recentQueries(v: unknown): RecentQuery[] {
	return Array.isArray(v) && v.every(isRecentQuery) ? v : [];
}

export function recentKey(orgId: string, workspaceId: string, view: ObservabilityView): string | null {
	return RECENT_VIEWS.has(view) ? `${RECENT_KEY_PREFIX}:${orgId}/${workspaceId}:${view}` : null;
}

export function recentQuery(view: ObservabilityView, tokens: Token[], text: string): string {
	const support = tokenSupport(view, tokens);
	return queryStr(
		tokens.filter((_, i) => support[i]?.ok === true),
		searchesText(view) ? text : "",
	);
}

export function addRecent(list: RecentQuery[], q: string, ts: number): RecentQuery[] {
	return [{ q, ts }, ...list.filter((r) => r.q !== q)].slice(0, RECENT_KEEP);
}

export function shownRecent(list: RecentQuery[], current: string): RecentQuery[] {
	return list.filter((r) => r.q !== current).slice(0, RECENT_SHOWN);
}

export function agoShort(ms: number, nowMs: number): string {
	const mins = Math.round((nowMs - ms) / MINUTE_MS);
	if (mins < 1) return "now";
	if (mins < HOUR_MINUTES) return `${mins.toString()}m`;
	if (mins < DAY_MINUTES) return `${Math.round(mins / HOUR_MINUTES).toString()}h`;
	return `${Math.round(mins / DAY_MINUTES).toString()}d`;
}
