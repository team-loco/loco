import { flattenJson } from "./format";
import type { LogRow } from "./rows";

export type ColumnKey = string;

export const PICKABLE_COLUMNS: ColumnKey[] = ["level", "resource", "replica", "region", "trace"];
export const DEFAULT_COLUMNS: ColumnKey[] = ["level", "resource"];

const LABELS: Record<string, string> = {
	level: "Level",
	resource: "Resource",
	replica: "Replica",
	region: "Region",
	trace: "Trace",
};

const WIDTHS: Record<string, number> = { level: 76, resource: 140, replica: 110, region: 112, trace: 128 };
const ATTRIBUTE_WIDTH = 150;
const TRACE_PREFIX = 12;

export function readColumns(value: string | null): ColumnKey[] {
	if (value === null) return DEFAULT_COLUMNS;
	return [...new Set(value.split(",").filter(Boolean))];
}

export function writeColumns(columns: ColumnKey[]): string | null {
	const same = columns.length === DEFAULT_COLUMNS.length && columns.every((c, i) => c === DEFAULT_COLUMNS[i]);
	return same ? null : columns.join(",");
}

export function columnLabel(key: ColumnKey): string {
	return LABELS[key] ?? key;
}

export function columnWidth(key: ColumnKey): number {
	return WIDTHS[key] ?? ATTRIBUTE_WIDTH;
}

export function rowAttributes(row: LogRow): Record<string, string> {
	return {
		...row.entry.resourceAttributes,
		...row.entry.logAttributes,
		...(row.json !== null ? flattenJson(row.json) : {}),
	};
}

export function columnValue(row: LogRow, key: ColumnKey): string {
	switch (key) {
		case "level":
			return row.label;
		case "resource":
			return row.resourceName;
		case "replica":
			return row.replica;
		case "region":
			return row.region;
		case "trace":
			return row.entry.traceId.slice(0, TRACE_PREFIX);
		default:
			return rowAttributes(row)[key] ?? "";
	}
}
