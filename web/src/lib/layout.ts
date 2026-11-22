import { useCallback } from "react";

import { useStoredJSON } from "./storage";

/** Every size the viewer sets (sidebar, drawers, trace panes, table columns), in one localStorage entry. */
const LAYOUT_KEY = "loco-layout";

type Layout = Record<string, unknown>;

function asLayout(value: unknown): Layout {
	return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Layout) : {};
}

function at(layout: Layout, path: readonly string[]): unknown {
	return path.reduce<unknown>((node, key) => asLayout(node)[key], layout);
}

function put(layout: Layout, [key = "", ...rest]: readonly string[], value: unknown): Layout {
	const next = rest.length === 0 ? value : put(asLayout(layout[key]), rest, value);
	const { [key]: _, ...others } = layout;
	return next === undefined || (typeof next === "object" && next !== null && Object.keys(next).length === 0)
		? others
		: { ...others, [key]: next };
}

/** A number in range, or `fallback`. */
export function layoutNumber(fallback: number, min: number, max: number): (value: unknown) => number {
	return (value) => (typeof value === "number" && value >= min && value <= max ? value : fallback);
}

/** The size stored at a dotted `path` of `loco-layout`, such as `sidebar.width`; null `path` stores nothing, and setting null forgets it. */
export function useLayout<T>(path: string | null, parse: (value: unknown) => T): [T, (next: T | null) => void] {
	const [layout, update] = useStoredJSON(path === null ? null : LAYOUT_KEY, asLayout);
	const keys = path?.split(".") ?? [];
	const set = useCallback(
		(next: T | null) => {
			if (path === null) return;
			update((current) => {
				const written = put(current, path.split("."), next ?? undefined);
				return Object.keys(written).length === 0 ? null : written;
			});
		},
		[path, update],
	);
	return [parse(path === null ? undefined : at(layout, keys)), set];
}
