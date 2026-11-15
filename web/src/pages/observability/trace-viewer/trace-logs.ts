import { traceBounds } from "./trace-time";
import { useMemo, useState } from "react";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { AROUND_MS } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";
import { useTraceResources } from "./TraceColors";
import { useObs } from "../context";
import { toRows } from "../rows";
import type { TraceSummary } from "../traces";

export function useTraceLogs(trace: TraceSummary) {
	const { color } = useTraceResources();
	const { workspaceId, scopedResourceIds, resourceById } = useObs();
	const { active } = useEnvironments();
	const [now] = useState(Date.now);
	const { start, end } = traceBounds(trace);
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

	const spans = useMemo(
		() => ({
			byId: new Map(trace.spans.map((span) => [span.spanId, span])),
			byResource: new Map(trace.spans.map((span) => [span.resourceId, span])),
		}),
		[trace],
	);
	const rows = useMemo(
		() =>
			toRows(query.entries, resourceById).map((row) => {
				const linked =
					row.entry.spanId === "" ? spans.byResource.get(row.entry.resourceId) : spans.byId.get(row.entry.spanId);
				return linked === undefined ? row : { ...row, color: color(linked) };
			}),
		[query.entries, resourceById, spans, color],
	);

	return { ...query, rows, from, to };
}
