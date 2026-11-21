import { ConnectError } from "@connectrpc/connect";
import { ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";

import { hasBoolean, queryError } from "@/pages/observability/query";
import { auditQuery } from "./model";

export interface AuditField {
	key: string;
	label: string;
}

/** The search fields the API lists in its rejection of an audit query. */
function auditFields(error: ConnectError): AuditField[] {
	const metadata = error.findDetails(ErrorInfoSchema).find((d) => d.reason === "QUERY_SYNTAX")?.metadata ?? {};
	const keys = metadata["fields"]?.split(",") ?? [];
	return keys.map((key) => ({ key, label: metadata[`@${key}`] ?? "" }));
}

export function auditSearchError(error: Error, types: readonly string[], actor: string, search: string) {
	const sent = auditQuery(types, actor, search);
	const rejected = queryError(error, sent);
	if (rejected === null || !(error instanceof ConnectError)) return null;
	const text = search.trim();
	const grouped = auditQuery(types, actor, "") !== "" && hasBoolean(text);
	const prefix = Array.from(sent).length - Array.from(text).length - (grouped ? 1 : 0);
	const leading = Array.from(search).length - Array.from(search.trimStart()).length;
	const chars = Array.from(search);
	const at = Math.max(0, Math.min(chars.length, rejected.position - 1 - prefix + leading));
	let start = at;
	let end = at;
	if (chars[at] !== undefined && !/[\s()]/.test(chars[at])) {
		while (start > 0 && !/[\s()]/.test(chars[start - 1] ?? "")) start--;
		while (end < chars.length && !/[\s()]/.test(chars[end] ?? "")) end++;
	} else end = Math.min(chars.length, at + 1);
	return { error: { ...rejected, query: search, position: start + 1 }, start, end, fields: auditFields(error) };
}

export function insertAuditField(search: string, start: number, end: number, field: string) {
	const chars = Array.from(search);
	const before = chars.slice(0, start).join("");
	const term = `${before !== "" && !/[\s(]$/.test(before) ? " " : ""}@${field}:`;
	return { query: before + term + chars.slice(end).join(""), caret: before.length + term.length };
}

/** A suggested fix for the rejected search term, or null when there is none to offer. */
export function auditQueryFix(term: string, message: string, fields: readonly AuditField[]): string | null {
	const key = /^-?@([\w.]*)/.exec(term)?.[1];
	if (key === undefined) return null;
	if (key === "environment") return "Pick a workspace, then use the Environment filter.";
	if (fields.some((f) => f.key === key)) {
		return message.includes("compare numbers") ? "Use an exact value or a * wildcard." : null;
	}
	const near = key === "" ? undefined : fields.find((f) => f.key.startsWith(key.slice(0, 2)));
	return near === undefined ? "Pick one of the fields listed." : `Did you mean @${near.key}?`;
}
