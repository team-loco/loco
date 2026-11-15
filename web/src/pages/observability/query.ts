import { Code, ConnectError } from "@connectrpc/connect";
import { createParser, parseAsArrayOf, parseAsString } from "nuqs";
import { BadRequestSchema, ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";

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

export type FieldKey = "resource" | "level" | "replica" | "build" | "region" | "type" | "status" | "trace" | "duration";

export interface FieldDef {
	key: FieldKey;
	label: string;
	views: ObservabilityView[];
	negate: ObservabilityView[];
	/** Views that apply only the first filter on this field. */
	single: ObservabilityView[];
}

const ALL_VIEWS: ObservabilityView[] = ["logs", "metrics", "traces", "events"];

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
];

const KEY_ALIASES: Record<string, string> = { pod: "replica", trace_id: "trace" };

export function fieldDef(key: string): FieldDef | undefined {
	return FIELDS.find((f) => f.key === key);
}

export function viewFields(view: ObservabilityView): FieldDef[] {
	return FIELDS.filter((f) => f.views.includes(view));
}

export function searchesText(view: ObservabilityView): boolean {
	return view !== "metrics";
}

/** A @key:value term; value is written as in the query, quotes included. */
export interface Token {
	neg: boolean;
	key: string;
	value: string;
}

const KEY = String.raw`[\w.\-/]+(?:\[\d+\])*`;
const VALUE = String.raw`(?:"(?:[^"\\]|\\.)*"|[^\s"()]+)`;
const TOKEN_RE = new RegExp(String.raw`(?<=^|\s)(-?)@(${KEY}):(${VALUE})(?=\s|$)`, "g");
export const COMPLETED_TOKEN_RE = new RegExp(String.raw`(^|\s)(-?)@(${KEY}):(${VALUE})\s$`);
const QUERY_KEY_RE = new RegExp(String.raw`^${KEY}$`);
const BARE_TOKEN_RE = /^(pod|replica|build):(\S+)$/;
const QUOTED_RE = /"(?:[^"\\]|\\.)*"/g;
const BOOLEAN_RE = /(^|\s)(AND|OR)(?=\s|$)|[()]/;
const BARE_VALUE_RE = /^[^\s"():*<>]+$/;
const FIELD_RE = new RegExp(String.raw`-?@${KEY}:${VALUE}`, "g");
const WORD_RE = /(-?)("(?:[^"\\]|\\.)*"|[^\s"()]+)/g;

export const BUILD_ID_LABEL = "loco.io/build-id";

/** Whether text combines terms with AND, OR or parentheses, outside quoted phrases. */
export function hasBoolean(text: string): boolean {
	return BOOLEAN_RE.test(text.replace(QUOTED_RE, '""'));
}

// keep in sync with the built-in fields and reserved keys in internal/clickhouse/log_filter.go
const BUILTIN_KEYS = new Set(["level", "trace", "resource", "replica", "build", "region"]);

/** Why an attribute key can't be filtered with @key:value, or null when it can. */
export function queryKeyReason(key: string): string | null {
	if (BUILTIN_KEYS.has(key)) return `@${key} is a built-in field, not this attribute`;
	if (key.startsWith("loco.") && !key.startsWith("loco.io/"))
		return `Loco sets ${key}; filter with @resource or @region`;
	if (!QUERY_KEY_RE.test(key)) return `${key} can't be written as a query key`;
	return null;
}

/** Writes value as a query value that matches it exactly. */
export function quoteValue(value: string): string {
	return BARE_VALUE_RE.test(value) && !value.includes("..")
		? value
		: `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}

/** Reads a query value as the text it matches, without quotes. */
export function unquoteValue(value: string): string {
	return value.startsWith('"') ? value.slice(1, -1).replace(/\\(["\\])/g, "$1") : value;
}

/** The free-text terms of text that matching log bodies contain, for highlighting. */
export function searchWords(text: string): string[] {
	return [...text.replace(FIELD_RE, " ").matchAll(WORD_RE)].flatMap(([, neg, word = ""]) =>
		neg !== "" || word === "AND" || word === "OR" ? [] : [unquoteValue(word)],
	);
}

function tokStr(t: Token): string {
	return `${t.neg ? "-" : ""}@${t.key}:${t.value}`;
}

/** Joins tokens and text into one query; text that uses AND, OR or parentheses is grouped. */
export function queryStr(tokens: Token[], text: string): string {
	const rest = text.trim();
	const grouped = tokens.length > 0 && hasBoolean(rest) ? `(${rest})` : rest;
	return [...tokens.map(tokStr), grouped].filter(Boolean).join(" ");
}

/** Splits a query into chips and text. A query with AND, OR or parentheses stays whole text, so its order holds. */
export function parseQuery(q: string): { tokens: Token[]; text: string } {
	const bare = BARE_TOKEN_RE.exec(q.trim());
	if (bare !== null) {
		return {
			tokens: [{ neg: false, key: KEY_ALIASES[bare[1] ?? ""] ?? bare[1] ?? "", value: bare[2] ?? "" }],
			text: "",
		};
	}
	if (hasBoolean(q)) return { tokens: [], text: q.replace(/\s+/g, " ").trim() };
	const tokens: Token[] = [];
	for (const m of q.matchAll(TOKEN_RE)) {
		const key = m[2] ?? "";
		tokens.push({ neg: m[1] === "-", key: KEY_ALIASES[key] ?? key, value: m[3] ?? "" });
	}
	return { tokens, text: q.replace(TOKEN_RE, "").replace(/\s+/g, " ").trim() };
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
	const seen = new Set<string>();
	return tokens.map((t) => {
		if (view === "logs") return SUPPORTED;
		const def = fieldDef(t.key);
		if (!def?.views.includes(view)) {
			return { ok: false, reason: `@${t.key} doesn't apply to ${view}` };
		}
		if (t.neg && !def.negate.includes(view)) return { ok: false, reason: `Excluding @${t.key} isn't supported yet` };
		if (def.single.includes(view) && seen.has(t.key)) {
			return { ok: false, reason: `Only the first @${t.key} filter applies` };
		}
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

/** A query the API rejected; position counts characters of query from 1. */
export interface QueryError {
	query: string;
	message: string;
	position: number;
}

/** Reads the API's rejection of query, or null when error is not one. */
export function queryError(error: Error, query: string): QueryError | null {
	if (!(error instanceof ConnectError) || error.code !== Code.InvalidArgument) return null;
	const violation = error
		.findDetails(BadRequestSchema)
		.flatMap((d) => d.fieldViolations)
		.find((v) => v.field === "query");
	const info = error.findDetails(ErrorInfoSchema).find((d) => d.reason === "LOG_QUERY_SYNTAX");
	const position = Number(info?.metadata["position"]);
	if (violation === undefined || !Number.isInteger(position)) return null;
	return { query, message: violation.description, position };
}

export interface BackendQuery {
	query: string;
	resourceIds: string[];
	allEnvironments: boolean;
	impossible: boolean;
}

/** The logs request for a query: the API parses the text; the scope comes from the caller's grants. */
export function buildBackendQuery(tokens: Token[], text: string, scopedResourceIds: string[] | null): BackendQuery {
	const resourceIds = scopedResourceIds ?? [];
	return {
		query: queryStr(tokens, text),
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
