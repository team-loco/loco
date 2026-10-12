import type { MessageInitShape } from "@bufbuild/protobuf";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { ClusterAccess } from "@gen/loco/observability/v1/observability_access_pb";
import type { KubernetesEvent, QueryEventsRequestSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp, tsMs } from "@/lib/time";

export const EVENT_WINDOW_MS = 86_400_000;
export const EVENT_LIMIT = 500;

const WARNING = "Warning";

export type Severity = "warning" | "normal";
export type TypeFilter = "all" | Severity;

export interface EventsTarget {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
}

export function eventProxyUrls(clusters: ClusterAccess[], deployments: Deployment[]): string[] {
	const clusterIds = new Set(deployments.map((d) => d.clusterId));
	const urls = clusters.filter((c) => clusterIds.has(c.clusterId) && c.proxyUrl !== "").map((c) => c.proxyUrl);
	return [...new Set(urls)].toSorted();
}

export function eventsKey(target: EventsTarget, proxyUrls: string[]) {
	const { workspaceId, environmentId, resourceId } = target;
	return ["resource-events", workspaceId, environmentId, resourceId, ...proxyUrls];
}

export function eventsRequest(target: EventsTarget, nowMs: number): MessageInitShape<typeof QueryEventsRequestSchema> {
	const { workspaceId, environmentId, resourceId } = target;
	return {
		workspaceId,
		environmentId,
		resourceIds: [resourceId],
		startTime: msToTimestamp(nowMs - EVENT_WINDOW_MS),
		endTime: msToTimestamp(nowMs),
		limit: EVENT_LIMIT,
	};
}

export function mergeEvents(perProxy: KubernetesEvent[][]): KubernetesEvent[] {
	return perProxy
		.flat()
		.toSorted((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp))
		.slice(0, EVENT_LIMIT);
}

export function severityOf(event: KubernetesEvent): Severity {
	return event.type === WARNING ? "warning" : "normal";
}

export function objectLabel(event: KubernetesEvent): string {
	return event.objectName === "" ? event.objectKind : `${event.objectKind}/${event.objectName}`;
}

export interface FilteredEvents {
	rows: KubernetesEvent[];
	counts: Record<TypeFilter, number>;
}

export function filterEvents(events: KubernetesEvent[], query: string, type: TypeFilter): FilteredEvents {
	const q = query.trim().toLowerCase();
	const counts: Record<TypeFilter, number> = { all: 0, warning: 0, normal: 0 };
	const rows: KubernetesEvent[] = [];
	for (const e of events) {
		const text = `${e.reason} ${e.message} ${objectLabel(e)}`.toLowerCase();
		if (q !== "" && !text.includes(q)) continue;
		const severity = severityOf(e);
		counts.all++;
		counts[severity]++;
		if (type === "all" || type === severity) rows.push(e);
	}
	return { rows, counts };
}
