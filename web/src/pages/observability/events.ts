import type { Glyph } from "@/components/design/StatusGlyph";
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

const REASON_TITLES: Record<string, string> = {
	BackOff: "Restarting after crash",
	CrashLoopBackOff: "Restarting after crash",
	Unhealthy: "Health check failing",
	ProbeWarning: "Health check warning",
	Pulling: "Pulling image",
	Pulled: "Image pulled",
	ErrImagePull: "Image pull failed",
	ImagePullBackOff: "Image pull failed",
	InvalidImageName: "Invalid image name",
	Failed: "Container failed",
	Created: "Container created",
	Started: "Container started",
	Killing: "Stopping container",
	Scheduled: "Replica scheduled",
	FailedScheduling: "Couldn't schedule replica",
	SuccessfulCreate: "Replica created",
	SuccessfulDelete: "Replica removed",
	FailedCreate: "Couldn't create replica",
	ScalingReplicaSet: "Scaled",
	OOMKilling: "Out of memory, replica killed",
	OOMKilled: "Out of memory, replica killed",
	Evicted: "Replica evicted",
	Preempting: "Replica preempted",
	FailedMount: "Volume mount failed",
	FailedAttachVolume: "Volume attach failed",
	NodeNotReady: "Node not ready",
	SandboxChanged: "Replica sandbox recreated",
	FailedKillPod: "Couldn't stop replica",
};

const WORD_BOUNDARY_RE = /([a-z0-9])([A-Z])/g;

export function eventTitle(reason: string): string {
	const known = REASON_TITLES[reason];
	if (known !== undefined) return known;
	const words = reason.replace(WORD_BOUNDARY_RE, "$1 $2").toLowerCase();
	return words === "" ? "Event" : words.charAt(0).toUpperCase() + words.slice(1);
}

export function severityOf(type: string, reason: string): Severity {
	if (type.toLowerCase() !== "warning") return "normal";
	if (ERROR_REASONS.has(reason) || reason.startsWith("Failed")) return "error";
	return "warning";
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
		const object = e.objectName !== "" ? e.objectName : e.resourceName;
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
	return [...m.values()].toSorted((a, b) => b.ts - a.ts);
}
