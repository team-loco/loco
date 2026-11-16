/** An audit query (docs/content/reference/audit-queries.md) matching any of the event types. */
export function eventTypesQuery(types: readonly string[]): string {
	return types.length === 0 ? "" : `(${types.map((t) => `@type:${t}`).join(" OR ")})`;
}
