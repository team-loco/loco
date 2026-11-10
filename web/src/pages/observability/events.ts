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
export function eventTitle(reason: string): string {
	return REASON_TITLES[reason] ?? (reason === "" ? "Service recovered" : reason.replaceAll("_", " "));
}
export function severityOf(type: string, reason: string): Severity {
	if (type !== "resource.degraded") return "normal";
	return reason === "health_check_failing" || reason === "unschedulable" ? "warning" : "error";
}

export interface SeverityStyle {
	glyph: Glyph;
	dot: string;
	fg: string;
	badge: string;
	label: string;
}

export function severityStyle(s: Severity): SeverityStyle {
	switch (s) {
		case "error":
			return {
				glyph: "err",
				dot: "bg-err",
				fg: "text-bad-fg",
				badge: "bg-bad-bg text-bad-fg",
				label: "Error",
			};
		case "warning":
			return {
				glyph: "warn",
				dot: "bg-warn",
				fg: "text-warn-fg",
				badge: "bg-warn-bg text-warn-fg",
				label: "Warning",
			};
		case "normal":
			return {
				glyph: "off",
				dot: "bg-line2",
				fg: "text-foreground",
				badge: "bg-neutral-bg text-neutral-fg",
				label: "Normal",
			};
	}
}

export interface EventGroup {
	key: string;
	resourceId: string;
	resourceName: string;
	reason: string;
	message: string;
	object: string;
	severity: Severity;
	type: string;
	ts: number;
	occ: number[];
}

export function groupEvents(events: WorkspaceEventWithResource[]): EventGroup[] {
	const m = new Map<string, EventGroup>();
	for (const e of events) {
		const object = e.resourceName;
		const key = `${e.resourceId}|${e.reason}|${e.placementId}|${object}|${e.message}`;
		const ts = tsMs(e.timestamp);
		const g = m.get(key);
		if (g !== undefined) {
			g.occ.push(ts);
			if (ts > g.ts) g.ts = ts;
		} else {
			m.set(key, {
				key,
				resourceId: e.resourceId,
				resourceName: e.resourceName,
				reason: e.reason,
				message: e.message,
				object,
				severity: severityOf(e.type, e.reason),
				type: e.type,
				ts,
				occ: [ts],
			});
		}
	}
	return [...m.values()].toSorted((a, b) => b.ts - a.ts);
}
