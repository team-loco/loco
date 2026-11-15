import { createParser, parseAsArrayOf, parseAsString } from "nuqs";

import type { ObservabilityView } from "@/lib/routes";

export type Level = "error" | "warn" | "info" | "debug";

export const LEVELS: Level[] = ["error", "warn", "info", "debug"];

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
	| "resource"
	| "level"
	| "replica"
	| "build"
	| "region"
	| "type"
	| "status"
	| "trace"
	| "duration"
	| "method"
	| "path"
	| "attr"
	| "label"
	| "json";

export interface FieldDef {
	key: FieldKey;
	label: string;
	views: ObservabilityView[];
	negate: ObservabilityView[];
	single: ObservabilityView[];
}

const ALL_VIEWS: ObservabilityView[] = ["logs", "metrics", "traces", "events"];

// @attr, @label and @json values are key=value pairs.
export const FIELDS: FieldDef[] = [
	{ key: "resource", label: "Resource", views: ALL_VIEWS, negate: ALL_VIEWS, single: [] },
	{ key: "level", label: "Level", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "replica", label: "Replica", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "build", label: "Build ID", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "region", label: "Region", views: ["logs", "metrics"], negate: ["logs", "metrics"], single: [] },
	{ key: "type", label: "Event type", views: ["events"], negate: ["events"], single: [] },
	{ key: "status", label: "Trace status", views: ["traces"], negate: ["traces"], single: [] },
	{ key: "trace", label: "Trace ID", views: ["logs", "traces"], negate: ["logs"], single: ["traces"] },
	{ key: "duration", label: "Duration (ms)", views: ["traces"], negate: [], single: [] },
	{ key: "method", label: "HTTP method", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "path", label: "Request path", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "attr", label: "Log attribute", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "label", label: "Resource attribute", views: ["logs"], negate: ["logs"], single: [] },
	{ key: "json", label: "JSON message field", views: ["logs"], negate: ["logs"], single: [] },
];

// The gateway's access logs carry these keys, so @method and @path match every routed request.
export const REQUEST_FILTER_ATTRIBUTES: Partial<Record<FieldKey, string>> = { method: "method", path: "path" };

const PAIR_FIELDS = new Set<FieldKey>(["attr", "label", "json"]);
const RESERVED_LABEL_PREFIX = "loco.";
const OPEN_LABEL_PREFIX = "loco.io/";

export interface AttributePair {
	key: string;
	value: string;
}

/** Reads an @attr, @label or @json value, written key=value. */
export function attributePair(value: string): AttributePair | null {
	const at = value.indexOf("=");
	return at > 0 ? { key: value.slice(0, at), value: value.slice(at + 1) } : null;
}

/** Why a log query can't filter on the resource attribute, or null when it can; Loco sets the loco. keys besides loco.io/ labels. */
export function reservedLabelReason(key: string): string | null {
	return key.startsWith(RESERVED_LABEL_PREFIX) && !key.startsWith(OPEN_LABEL_PREFIX)
		? `Loco sets ${key}; filter with @resource or @region`
		: null;
}

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
	return FIELDS.filter((f) => f.views.includes(view));
}

export function searchesText(view: ObservabilityView): boolean {
	return view !== "metrics";
}

export interface Token {
	neg: boolean;
	key: FieldKey;
	value: string;
}

const TOKEN_VALUE = String.raw`("(?:[^"\\]|\\.)*"|\S+)`;
const TOKEN_RE = new RegExp(String.raw`(-?)@(\w+):` + TOKEN_VALUE, "g");
export const COMPLETED_TOKEN_RE = new RegExp(String.raw`(^|\s)(-?)@(\w+):` + TOKEN_VALUE + String.raw`\s$`);
const QUOTED_RE = /^"(?:[^"\\]|\\.)*"$/;
const NEEDS_QUOTES_RE = /[\s"]/;
const BARE_TOKEN_RE = /^(pod|replica|build):(\S+)$/;

export const BUILD_ID_LABEL = "loco.io/build-id";

/** Reads a token value as written, unquoting one that holds spaces or quotes. */
export function tokenValue(raw: string): string {
	return QUOTED_RE.test(raw) ? (JSON.parse(raw) as string) : raw;
}

function tokStr(t: Token): string {
	return `${t.neg ? "-" : ""}@${t.key}:${NEEDS_QUOTES_RE.test(t.value) ? JSON.stringify(t.value) : t.value}`;
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
		if (key !== undefined) tokens.push({ neg: m[1] === "-", key, value: tokenValue(m[3] ?? "") });
	}
	const text = q
		.replace(TOKEN_RE, (all: string, _neg: string, key: string) => (fieldKey(key) !== undefined ? "" : all))
		.replace(/\s+/g, " ")
		.trim();
	return { tokens, text };
}

export interface Query {
	tokens: Token[];
	text: string;
}

// q holds the query text; r is a comma list of resource names that resource links set.
export const QUERY_PARSERS = {
	q: createParser<Query>({
		parse: parseQuery,
		serialize: ({ tokens, text }) => queryStr(tokens, text),
		eq: (a, b) => queryStr(a.tokens, a.text) === queryStr(b.tokens, b.text),
	}).withDefault({ tokens: [], text: "" }),
	r: parseAsArrayOf(parseAsString).withDefault([]),
};

export function queryValues(tokens: Token[], text: string): { q: Query; r: null } {
	return { q: { tokens, text }, r: null };
}

export function toQuery({ q, r }: { q: Query; r: string[] }): Query {
	const named = r.filter(Boolean).map((value): Token => ({ neg: false, key: "resource", value }));
	return { tokens: [...named, ...q.tokens], text: q.text };
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
		if (!def.views.includes(view)) return { ok: false, reason: `@${t.key} doesn't apply to ${view}` };
		if (t.neg && !def.negate.includes(view)) return { ok: false, reason: `Excluding @${t.key} isn't supported yet` };
		const pair = PAIR_FIELDS.has(t.key) ? attributePair(t.value) : null;
		if (PAIR_FIELDS.has(t.key) && pair === null) return { ok: false, reason: `Write @${t.key}:key=value` };
		const blocked = t.key === "label" && pair !== null ? reservedLabelReason(pair.key) : null;
		if (blocked !== null) return { ok: false, reason: blocked };
		if (def.single.includes(view) && seen.has(t.key))
			return { ok: false, reason: `Only the first @${t.key} filter applies` };
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
	query: string;
	resourceIds: string[];
	allEnvironments: boolean;
	impossible: boolean;
}

/** The logs request for a query: the API parses its text; the scope comes from the caller's grants. */
export function buildBackendQuery(tokens: Token[], text: string, scopedResourceIds: string[] | null): BackendQuery {
	const terms = tokens.map((t) => {
		const pair = PAIR_FIELDS.has(t.key) ? attributePair(t.value) : null;
		if (pair === null) return t;
		const value = /^[^\s"():]+$/.test(pair.value) ? pair.value : JSON.stringify(pair.value);
		return { ...t, key: pair.key as FieldKey, value };
	});
	const resourceIds = scopedResourceIds ?? [];
	return {
		query: queryStr(terms, text),
		resourceIds,
		allEnvironments: tokens.some((t) => t.key === "build" && !t.neg),
		impossible: scopedResourceIds !== null && resourceIds.length === 0,
	};
}

const PINNED_KEY_PREFIX = "loco:pinned-queries:v1";
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

export function pinnedKey(orgId: string, workspaceId: string, view: ObservabilityView): string {
	return `${PINNED_KEY_PREFIX}:${orgId}/${workspaceId}:${view}`;
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

export function shownRecent(list: RecentQuery[], current: string, pinned: string[]): RecentQuery[] {
	return list.filter((r) => r.q !== current && !pinned.includes(r.q)).slice(0, RECENT_SHOWN);
}

export function agoShort(ms: number, nowMs: number): string {
	const mins = Math.round((nowMs - ms) / MINUTE_MS);
	if (mins < 1) return "now";
	if (mins < HOUR_MINUTES) return `${mins.toString()}m`;
	if (mins < DAY_MINUTES) return `${Math.round(mins / HOUR_MINUTES).toString()}h`;
	return `${Math.round(mins / DAY_MINUTES).toString()}d`;
}
