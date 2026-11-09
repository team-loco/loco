import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import { resolveWindow, type TimeWindow } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";

interface ResourceEventsInput {
	workspaceId: string;
	resourceId: string;
	environmentId?: string;
	window: TimeWindow;
	limit: number;
}

// The window resolves when the query runs, so a refetch of a relative window reaches the present.
export function resourceEventsOptions(
	transport: Transport,
	{ workspaceId, resourceId, environmentId = "", window, limit }: ResourceEventsInput,
) {
	const input = { workspaceId, resourceId, environmentId, limit };
	return {
		queryKey: [
			...createConnectQueryKey({
				schema: ObservabilityProxyService.method.queryEvents,
				transport,
				cardinality: "finite",
				input,
			}),
			window,
		],
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const { from, to } = resolveWindow(window, Date.now());
			const client = createClient(ObservabilityProxyService, transport);
			const request = { ...input, startTime: msToTimestamp(from), endTime: msToTimestamp(to) };
			return await client.queryEvents(request, { signal });
		},
		enabled: workspaceId !== "" && resourceId !== "",
	};
}
