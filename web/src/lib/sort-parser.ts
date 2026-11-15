import { createParser } from "nuqs";

export interface Sort<Id extends string> {
	id: Id;
	desc: boolean;
}

/** Reads a sort written as "id.asc" or "id.desc", accepting only the given ids. */
export function sortParser<const Id extends string>(ids: readonly Id[], fallback: Sort<Id>) {
	return createParser<Sort<Id>>({
		parse: (raw) => {
			const at = raw.lastIndexOf(".");
			const id = ids.find((i) => i === raw.slice(0, at));
			const dir = raw.slice(at + 1);
			return id !== undefined && (dir === "asc" || dir === "desc") ? { id, desc: dir === "desc" } : null;
		},
		serialize: (sort) => `${sort.id}.${sort.desc ? "desc" : "asc"}`,
		eq: (a, b) => a.id === b.id && a.desc === b.desc,
	}).withDefault(fallback);
}
