import type { Token } from "./query";

/** The Events view's chips and text as an audit query: chips of one key match any of their values, different keys all apply, a negated chip excludes, and the system actor is an actor type. */
export function eventsQuery(tokens: readonly Token[], text: string): string {
	const term = (t: Token) =>
		t.key === "actor" && t.value.toLowerCase() === "system" ? "@actor_type:system" : `@${t.key}:${t.value}`;
	const keys = [...new Set(tokens.map((t) => t.key))];
	const parts = keys.flatMap((key) => {
		const pos = tokens.filter((t) => t.key === key && !t.neg).map(term);
		const neg = tokens.filter((t) => t.key === key && t.neg).map((t) => `-${term(t)}`);
		return [...(pos.length > 1 ? [`(${pos.join(" OR ")})`] : pos), ...neg];
	});
	const rest = text.trim();
	return [...parts, rest !== "" && parts.length > 0 ? `(${rest})` : rest].filter(Boolean).join(" ");
}
