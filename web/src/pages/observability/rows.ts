import type { LogEntry } from "@gen/loco/observability/v1/observability_pb";

import { tsMs } from "@/lib/time";

import type { ObsResource } from "./context";
import { parseJsonMsg, type JsonObject } from "./format";
import { levelOf, type Level } from "./query";

export interface LogRow {
	key: string;
	ts: number;
	level: Level;
	label: string;
	resourceId: string;
	resourceName: string;
	color: string;
	pod: string;
	replica: string;
	region: string;
	entry: LogEntry;
	json: JsonObject | null;
}

function hashString(s: string): string {
	let h = 0;
	for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
	return (h >>> 0).toString(36);
}

function podName(entry: LogEntry): string {
	return entry.resourceAttributes["k8s.pod.name"] ?? "";
}

function replicaOf(pod: string): string {
	const parts = pod.split("-");
	return parts.length > 1 ? (parts.at(-1) ?? pod) : pod;
}

export function toRows(
	sources: { entries: LogEntry[]; region: string }[],
	resourceById: Map<string, ObsResource>,
): LogRow[] {
	const seen = new Map<string, number>();
	const rows: LogRow[] = [];
	for (const src of sources) {
		for (const entry of src.entries) {
			const ts = tsMs(entry.timestamp);
			const pod = podName(entry);
			const base = `${entry.timestamp?.seconds.toString() ?? "0"}.${String(entry.timestamp?.nanos ?? 0)}|${entry.resourceId}|${pod}|${hashString(entry.body)}`;
			const n = seen.get(base) ?? 0;
			seen.set(base, n + 1);
			const res = resourceById.get(entry.resourceId);
			const sev = entry.severity.toUpperCase();
			const level = levelOf(entry.severity);
			rows.push({
				key: `${base}#${String(n)}`,
				ts,
				level,
				label: sev === "" ? "INFO" : sev.length > 5 ? level.toUpperCase() : sev,
				resourceId: entry.resourceId,
				resourceName: res?.name ?? (entry.resourceId.slice(0, 8) || "unknown"),
				color: res?.color ?? "var(--fg4)",
				pod,
				replica: replicaOf(pod),
				region: src.region,
				entry,
				json: parseJsonMsg(entry.body),
			});
		}
	}
	return rows;
}
