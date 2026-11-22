import { useStoredJSON } from "./storage";

/** Every size the viewer sets (sidebar, drawers, trace panes, table columns), in one localStorage entry. */
const LAYOUT_KEY = "loco-layout";

type Layout = Record<string, unknown>;

function asLayout(value: unknown): Layout {
	return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Layout) : {};
}

/** A number in range, or `fallback`. */
export function layoutNumber(fallback: number, min: number, max: number): (value: unknown) => number {
	return (value) => (typeof value === "number" && value >= min && value <= max ? value : fallback);
}

/**
 * The size stored under `name` in `loco-layout`, such as `sidebar`; a null `name` stores nothing.
 * The setter takes a value or a function of the stored value, and null forgets it.
 */
export function useLayout<T>(
	name: string | null,
	parse: (value: unknown) => T,
): [T, (next: T | null | ((current: T) => T | null)) => void] {
	const [layout, update] = useStoredJSON(name === null ? null : LAYOUT_KEY, asLayout);
	const set = (next: T | null | ((current: T) => T | null)) => {
		if (name === null) return;
		update((current) => {
			const { [name]: stored, ...rest } = current;
			const value = typeof next === "function" ? (next as (current: T) => T | null)(parse(stored)) : next;
			return value === null ? rest : { ...rest, [name]: value };
		});
	};
	return [parse(name === null ? undefined : layout[name]), set];
}
