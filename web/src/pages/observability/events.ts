import type { Glyph } from "@/components/design/StatusGlyph";
import type { WorkspaceEventWithResource } from "@/hooks/useWorkspaceEvents";
import type { RuntimeEvent } from "@/lib/runtime-events";
import { tsMs } from "@/lib/time";
import { formatShortId } from "@/lib/utils";

export type Severity = "error" | "warning" | "normal";

export function eventDataLabel(key: string): string {
	return key.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();
}

export function eventSummary(e: RuntimeEvent): string {
	if (e.message !== "") return e.message;
	if (e.type === "resource.degraded") return e.reason;
	if (e.type === "resource.scaled")
		return [
			["replicas", e.replicas],
			["cpu", e.cpu],
			["memory", e.memory],
			["region", e.region],
		]
			.filter(([, value]) => value !== "")
			.map(([key, value]) => `${key} ${value}`)
			.join(" · ");
	for (const [key, label] of [
		["deploymentId", "deployment"],
		["buildId", "build"],
	] as const) {
		const value = e.event.data?.[key];
		if (typeof value === "string" && value !== "") return `${label} ${formatShortId(value)}`;
	}
	return "";
}

/** The API's severity for an event; an unknown value shows as normal. */
export function asSeverity(value: string): Severity {
	return value === "error" || value === "warning" ? value : "normal";
}

export interface SeverityStyle {
	glyph: Glyph;
	fg: string;
}

export function severityStyle(s: Severity): SeverityStyle {
	switch (s) {
		case "error":
			return {
				glyph: "err",
				fg: "text-bad-fg",
			};
		case "warning":
			return {
				glyph: "warn",
				fg: "text-warn-fg",
			};
		case "normal":
			return {
				glyph: "off",
				fg: "text-foreground",
			};
	}
}

export interface EventItem extends WorkspaceEventWithResource {
	detail: string;
	severity: Severity;
	ts: number;
}

export function eventItem(e: WorkspaceEventWithResource): EventItem {
	return {
		...e,
		detail: eventSummary(e),
		severity: asSeverity(e.severity),
		ts: tsMs(e.timestamp),
	};
}

export function eventValue(e: EventItem, key: string): string {
	switch (key) {
		case "resource":
			return e.resourceName;
		case "severity":
			return e.severity;
		case "type":
			return e.type;
		case "region":
			return e.region;
		case "reason":
			return e.reason;
		case "actor":
			return e.event.actorType === "system" ? "system" : e.event.actorName;
		default:
			return "";
	}
}

export function eventJson(e: EventItem): string {
	return JSON.stringify(
		{
			seq: e.event.seq.toString(),
			id: e.id,
			type: e.type,
			actor_type: e.event.actorType,
			actor_name: e.event.actorName,
			subject_type: e.event.subjectType,
			request_id: e.event.requestId,
			data: e.event.data ?? {},
			created_at: new Date(e.ts).toISOString(),
		},
		null,
		2,
	);
}
