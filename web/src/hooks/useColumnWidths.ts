import {
	useCallback,
	useRef,
	useState,
	useSyncExternalStore,
	type CSSProperties,
} from "react";
import {
	fitColumnWidths,
	resizeColumnPair,
	validColumnWidths,
} from "@/lib/column-widths";
import { readStorage, writeStorage } from "@/lib/storage";

const NARROW_TABLE_QUERY = "(width < 40rem)";
function subscribeNarrow(onChange: () => void) {
	const query = window.matchMedia(NARROW_TABLE_QUERY);
	query.addEventListener("change", onChange);
	return () => {
		query.removeEventListener("change", onChange);
	};
}
function narrowSnapshot() {
	return window.matchMedia(NARROW_TABLE_QUERY).matches;
}
export function useNarrowTable() {
	return useSyncExternalStore(subscribeNarrow, narrowSnapshot);
}

interface ColumnLayout {
	minimums: readonly number[];
	initial: (width: number) => readonly number[];
}
function preferences(key: string): Record<string, unknown> {
	try {
		const stored: unknown = JSON.parse(readStorage(key) ?? "{}");
		return typeof stored === "object" && stored !== null && !Array.isArray(stored)
			? stored as Record<string, unknown>
			: {};
	} catch {
		return {};
	}
}
function columnStyle(widths: readonly number[]): CSSProperties {
	return Object.fromEntries(
		widths.map((width, index) => [
			`--column-${String(index)}`,
			`${String(width)}px`,
		]),
	);
}
export function useColumnWidths(
	key: string,
	layoutName: string,
	layout: ColumnLayout,
) {
	const [saved, setSaved] = useState(() => preferences(key));
	const [available, setAvailable] = useState(0);
	const elementRef = useRef<HTMLElement | null>(null);
	const ref = useCallback((node: HTMLElement | null) => {
		elementRef.current = node;
		if (node === null) return;
		const measure = () => {
			setAvailable(node.clientWidth);
		};
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(node);
		return () => {
			observer.disconnect();
			elementRef.current = null;
		};
	}, []);
	const minimum = layout.minimums.reduce((sum, width) => sum + width, 0);
	const total = Math.max(available, minimum);
	const stored = saved[layoutName];
	const weights = validColumnWidths(stored, layout.minimums.length)
		? stored
		: layout.initial(total);
	const widths = fitColumnWidths(weights, layout.minimums, total);
	const style = {
		...columnStyle(widths),
		"--column-min-width": `${String(minimum)}px`,
	};
	const resize = (index: number, value: number, commit: boolean) => {
		const next = resizeColumnPair(widths, layout.minimums, index, value);
		for (const [name, width] of Object.entries(columnStyle(next))) {
			elementRef.current?.style.setProperty(name, String(width));
		}
		if (commit) {
			const updated = { ...saved, [layoutName]: next };
			setSaved(updated);
			writeStorage(key, JSON.stringify(updated));
		}
	};
	const handles = widths.slice(0, -1).map((value, index) => ({
		value,
		min: layout.minimums[index] ?? 0,
		max: value + (widths[index + 1] ?? 0) - (layout.minimums[index + 1] ?? 0),
	}));
	return { ref, style, widths, handles, resize };
}
