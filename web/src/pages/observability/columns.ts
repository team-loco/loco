import { flattenJson } from "./format";
import type { LogRow } from "./rows";

export type ColumnKey = string;

export const PICKABLE_COLUMNS: ColumnKey[] = [
	"level",
	"resource",
	"replica",
	"region",
	"trace",
	"method",
	"path",
	"status",
	"duration",
];
export const DEFAULT_COLUMNS: ColumnKey[] = ["level", "resource"];

const LABELS: Record<string, string> = {
	level: "Level",
	resource: "Resource",
	replica: "Replica",
	region: "Region",
	trace: "Trace",
	method: "Method",
	path: "Path",
	status: "Status",
	duration: "Duration",
};

// Request columns read the first log attribute an app commonly writes, OpenTelemetry names first.
const REQUEST_ATTRIBUTES: Record<string, string[]> = {
	method: ["http.request.method", "http.method", "method"],
	path: ["url.path", "http.route", "http.target", "path"],
	status: ["http.response.status_code", "http.status_code", "status"],
	duration: ["duration", "duration_ms", "latency"],
};

const WIDTHS: Record<string, number> = {
	level: 76,
	resource: 140,
	replica: 110,
	region: 112,
	trace: 128,
	method: 96,
	path: 180,
	status: 84,
	duration: 108,
};
const ATTRIBUTE_WIDTH = 150;
export const MIN_COLUMN_WIDTH = 56;
const MAX_COLUMN_WIDTH = 640;
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

export type ColumnWidths = Record<ColumnKey, number>;

export function columnWidth(key: ColumnKey, widths: ColumnWidths = {}): number {
	return widths[key] ?? WIDTHS[key] ?? ATTRIBUTE_WIDTH;
}

export function clampWidth(width: number): number {
	return Math.round(Math.min(MAX_COLUMN_WIDTH, Math.max(MIN_COLUMN_WIDTH, width)));
}

/** Reads widths written as key:px pairs, like "resource:200,path:320". */
export function readWidths(value: string | null): ColumnWidths {
	const widths: ColumnWidths = {};
	for (const pair of (value ?? "").split(",")) {
		const at = pair.lastIndexOf(":");
		const width = Number(pair.slice(at + 1));
		if (at > 0 && Number.isFinite(width)) widths[pair.slice(0, at)] = clampWidth(width);
	}
	return widths;
}

export function writeWidths(widths: ColumnWidths): string | null {
	const pairs = Object.entries(widths).map(([key, width]) => `${key}:${String(width)}`);
	return pairs.length === 0 ? null : pairs.join(",");
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
		default: {
			const attrs = rowAttributes(row);
			const names = REQUEST_ATTRIBUTES[key] ?? [key];
			return names.map((name) => attrs[name]).find((v) => v !== undefined && v !== "") ?? "";
		}
	}
}
