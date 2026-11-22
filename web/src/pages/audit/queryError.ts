import { hasBoolean, queryError } from "@/pages/observability/query";
import { auditQuery } from "./model";

/** The API's rejection of the audit search, with its position and term mapped onto search. */
export function auditSearchError(error: Error, types: readonly string[], actor: string, search: string) {
	const sent = auditQuery(types, actor, search);
	const rejected = queryError(error, sent);
	if (rejected === null) return null;
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
	return { error: { ...rejected, query: search, position: start + 1 }, start, end };
}
