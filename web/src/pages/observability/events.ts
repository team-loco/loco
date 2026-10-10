import type { WorkspaceEventWithResource } from "@/hooks/useWorkspaceEvents";
import { tsMs } from "@/lib/time";


export type Severity = "error" | "warning" | "normal";

const ERROR_REASONS = new Set([
	"BackOff",
	"CrashLoopBackOff",
	"OOMKilling",
	"OOMKilled",
	"Evicted",
	"ErrImagePull",
	"ImagePullBackOff",
	"InvalidImageName",
]);

export function severityOf(type: string, reason: string): Severity {
	if (type.toLowerCase() !== "warning") return "normal";
	if (ERROR_REASONS.has(reason) || reason.startsWith("Failed")) return "error";
	return "warning";
}

export interface SeverityStyle {
	dot: string;
	fg: string;
	badge: string;
	count: string;
	bar: string;
	label: string;
}

export function severityStyle(s: Severity): SeverityStyle {
	switch (s) {
		case "error":
			return { dot: "bg-err", fg: "text-bad-fg", badge: "bg-bad-bg text-bad-fg", count: "bg-bad-bg text-bad-fg", bar: "border-l-err", label: "Error" };
		case "warning":
			return { dot: "bg-warn", fg: "text-warn-fg", badge: "bg-warn-bg text-warn-fg", count: "bg-warn-bg text-warn-fg", bar: "border-l-warn", label: "Warning" };
		case "normal":
			return { dot: "bg-line2", fg: "text-foreground", badge: "bg-neutral-bg text-neutral-fg", count: "bg-bg3 text-fg2", bar: "border-l-transparent", label: "Normal" };
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
		const object = e.podName !== "" ? e.podName : e.resourceName;
		const key = `${e.resourceId}|${e.reason}|${object}|${e.message}`;
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
	return [...m.values()].sort((a, b) => b.ts - a.ts);
}
