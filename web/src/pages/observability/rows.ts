import type { LogEntry } from "@gen/loco/observability/v1/observability_pb";

import { formatIsoTimestamp, tsMs, type TimePreferences } from "@/lib/time";

import type { ObsResource } from "./context";
import { parseJsonMsg, type JsonObject } from "./format";
import { renderTemplate } from "./message-template";
import { BUILD_ID_LABEL, logLevel, type Level } from "./query";

export interface LogRow {
	key: string;
	ts: number;
	level: Level;
	resourceId: string;
	/** The Loco resource's name or, for a workload Loco did not create, its Kubernetes workload name. */
	resourceName: string;
	/** Whether the line comes from a Kubernetes workload that is not a Loco resource. */
	workload: boolean;
	color: string;
	pod: string;
	replica: string;
	region: string;
	build: string;
	entry: LogEntry;
	/** The body with its {name} placeholders filled from the log attributes. */
	message: string;
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

export const WORKLOAD_TITLE = "Kubernetes workload, not a Loco resource";

const WORKLOAD_ATTRIBUTES = [
	"k8s.deployment.name",
	"k8s.statefulset.name",
	"k8s.daemonset.name",
	"k8s.cronjob.name",
	"k8s.job.name",
	"k8s.pod.name",
];

function workloadName(entry: LogEntry): string {
	for (const key of WORKLOAD_ATTRIBUTES) {
		const name = entry.resourceAttributes[key];
		if (name !== undefined && name !== "") return name;
	}
	return "";
}

export function replicaOf(pod: string): string {
	const parts = pod.split("-");
	return parts.length > 1 ? (parts.at(-1) ?? pod) : pod;
}

export function toRows(entries: LogEntry[], resourceById: Map<string, ObsResource>): LogRow[] {
	const seen = new Map<string, number>();
	const rows: LogRow[] = [];
	for (const entry of entries) {
		const ts = tsMs(entry.timestamp);
		const pod = podName(entry);
		const base = `${entry.timestamp?.seconds.toString() ?? "0"}.${String(entry.timestamp?.nanos ?? 0)}|${entry.resourceId}|${pod}|${hashString(entry.body)}`;
		const n = seen.get(base) ?? 0;
		seen.set(base, n + 1);
		const res = resourceById.get(entry.resourceId);
		const workload = entry.resourceId === "";
		rows.push({
			key: `${base}#${String(n)}`,
			ts,
			level: logLevel(entry.level),
			resourceId: entry.resourceId,
			resourceName: workload ? workloadName(entry) : (res?.slug ?? entry.resourceId.slice(0, 8)),
			workload,
			color: res?.color ?? "var(--fg4)",
			pod,
			replica: replicaOf(pod),
			region: entry.resourceAttributes["loco.region"] ?? "",
			build: entry.resourceAttributes[BUILD_ID_LABEL] ?? "",
			entry,
			message: renderTemplate(entry.body, entry.logAttributes),
			json: parseJsonMsg(entry.body),
		});
	}
	return rows;
}

export function visibleLogRows(batch: LogRow[], tail: LogRow[], paused: LogRow[] | null = null): LogRow[] {
	if (paused !== null) return paused;
	const batchKeys = new Set(batch.map((row) => row.key));
	return [...tail.filter((row) => !batchKeys.has(row.key)), ...batch].toSorted((a, b) => b.ts - a.ts);
}

/** The row as the JSON the copy actions write; ids the line does not carry are left out. */
export function logJson(row: LogRow, pref: TimePreferences): string {
	const e = row.entry;
	return JSON.stringify(
		{
			timestamp: formatIsoTimestamp(row.ts, pref),
			level: row.level,
			message: row.json ?? row.message,
			[row.workload ? "workload" : "resource"]: row.resourceName,
			replica: row.pod,
			region: row.region,
			...(e.traceId === "" ? {} : { trace_id: e.traceId }),
			...(e.spanId === "" ? {} : { span_id: e.spanId }),
			resource_attributes: e.resourceAttributes,
			log_attributes: e.logAttributes,
		},
		null,
		2,
	);
}

export function logMessageText(row: LogRow): string {
	return row.json === null ? row.message : JSON.stringify(row.json, null, 2);
}
