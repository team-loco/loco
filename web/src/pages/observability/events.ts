import { resolveWindow, type TimeWindow } from "@/lib/obs-time";
import type { Glyph } from "@/components/design/StatusGlyph";
import type { WorkspaceEventWithResource } from "@/hooks/useWorkspaceEvents";
import { tsMs } from "@/lib/time";

export type Severity = "error" | "warning" | "normal";

// Public reason strings are owned by k8sapi/v1alpha1/runtime.go.
const REASON_TITLES: Record<string, string> = {
	crash_looping: "Restarting after crash",
	out_of_memory: "Out of memory",
	image_pull_failed: "Image pull failed",
	unschedulable: "Couldn't schedule service",
	health_check_failing: "Health check failing",
};

export function eventReasonLabel(reason: string): string {
	return REASON_TITLES[reason] ?? reason.replaceAll("_", " ");
}

export function eventDataLabel(key: string): string {
	return key.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();
}

const TYPE_TITLES: Record<string, string> = {
	"resource.degraded": "Degraded",
	"resource.recovered": "Recovered",
	"resource.restarted": "Restarted",
	"resource.scaled": "Scaled",
	"deployment.created": "Deploy created",
	"deployment.ready": "Deploy ready",
	"deployment.failed": "Deploy failed",
	"build.created": "Build queued",
	"build.started": "Build started",
	"build.succeeded": "Build succeeded",
	"build.failed": "Build failed",
	"build.canceled": "Build canceled",
};
const REGIONAL_TITLES = new Set([
	"resource.degraded",
	"resource.recovered",
	"resource.restarted",
	"deployment.ready",
	"deployment.failed",
]);

export function eventTitle(type: string, region = ""): string {
	return `${TYPE_TITLES[type] ?? type}${REGIONAL_TITLES.has(type) && region !== "" ? ` in ${region}` : ""}`;
}

export function eventSummary(e: WorkspaceEventWithResource): string {
	if (e.message !== "") return e.message;
	if (e.type === "resource.degraded") return eventReasonLabel(e.reason);
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
		if (typeof value === "string" && value !== "") return `${label} ${value.slice(0, 8)}`;
	}
	return "";
}

export function severityOf(type: string): Severity {
	if (type === "build.failed" || type === "deployment.failed") return "error";
	return type === "resource.degraded" ? "warning" : "normal";
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
	title: string;
	detail: string;
	severity: Severity;
	ts: number;
}

export function eventItem(e: WorkspaceEventWithResource): EventItem {
	return {
		...e,
		title: eventTitle(e.type, e.region),
		detail: eventSummary(e),
		severity: severityOf(e.type),
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

export function eventWindow(range: TimeWindow, now: number, latest: number) {
	return resolveWindow(range, Math.max(now, latest));
}
