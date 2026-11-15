import { useState } from "react";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { AROUND_MS } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";
import { useObs } from "../context";
import { toRows } from "../rows";
import type { TraceSummary } from "../traces";

export function useTraceLogs(trace: TraceSummary) {
	const { workspaceId, scopedResourceIds, resourceById } = useObs();
	const { active } = useEnvironments();
	const [now] = useState(Date.now);
	const start = Number(trace.start / 1_000_000n);
	const end = start + Number(trace.duration) / 1_000_000;
	const from = start - AROUND_MS;
	const to = Math.min(now, end + AROUND_MS);
	const query = useQueryLogs({
		workspaceId,
		environmentId: active?.id ?? "",
		resourceIds: scopedResourceIds ?? [],
		spanMs: to - from,
		query: `@trace:${trace.id}`,
		order: LogOrder.OLDEST_FIRST,
		startTime: msToTimestamp(from),
		endTime: msToTimestamp(to),
		limit: 50,
	});
	return { ...query, rows: toRows(query.entries, resourceById), from, to };
}
export function descendantIds(trace: TraceSummary, id: string): Set<string> {
	const children = new Map<string, string[]>();
	for (const s of trace.spans) {
		const group = children.get(s.parentSpanId) ?? [];
		group.push(s.spanId);
		children.set(s.parentSpanId, group);
	}
	const ids = new Set<string>();
	const todo = [id];
	while (todo.length > 0) {
		const next = todo.pop();
		if (next === undefined || ids.has(next)) continue;
		ids.add(next);
		todo.push(...(children.get(next) ?? []));
	}
	return ids;
}
