import { useQuery } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityService, type TraceStatus } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";
import type { TraceScopeOptions } from "./useGetTrace";

interface TraceBucketsOptions extends TraceScopeOptions {
	start: number;
	end: number;
	traceId: string;
	rootNameTerms: string[];
	status: TraceStatus;
	intervalSeconds: number;
	minDurationNanos?: bigint;
	maxDurationNanos?: bigint;
}

export function useTraceBuckets(options: TraceBucketsOptions) {
	const transport = useTransport();
	const { enabled = true, start, end, workspaceId, environmentId, resourceIds, ...filters } = options;
	const input = {
		...filters,
		scope: { workspaceId, environmentId, resourceIds },
		startTime: msToTimestamp(start),
		endTime: msToTimestamp(end),
	};
	return useQuery({
		queryKey: createConnectQueryKey({
			schema: ObservabilityService.method.queryTraceBuckets,
			transport,
			cardinality: "finite",
			input,
		}),
		queryFn: async ({ signal }) =>
			await createClient(ObservabilityService, transport).queryTraceBuckets(input, { signal }),
		enabled: enabled && workspaceId !== "" && environmentId !== "",
		staleTime: Infinity,
	});
}
