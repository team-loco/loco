import { useQuery } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { type MessageInitShape } from "@bufbuild/protobuf";
import { ObservabilityProxyService, type TraceSummaryQuerySchema } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";
import type { TraceQueryOptions } from "./useQueryTraces";

type SummaryOptions = TraceQueryOptions & {
	limit: number;
	summary: MessageInitShape<typeof TraceSummaryQuerySchema>;
};

export function traceSummaryQueryOptions(transport: Transport, options: SummaryOptions) {
	const { enabled = true, start, end, ...scope } = options;
	const input = { ...scope, startTime: msToTimestamp(start), endTime: msToTimestamp(end) };
	return {
		queryKey: createConnectQueryKey({
			schema: ObservabilityProxyService.method.queryTraces,
			transport,
			cardinality: "finite",
			input,
		}),
		queryFn: async ({ signal }: { signal: AbortSignal }) =>
			await createClient(ObservabilityProxyService, transport).queryTraces(input, { signal }),
		enabled: enabled && options.workspaceId !== "" && options.environmentId !== "",
		staleTime: Infinity,
	};
}

export function useTraceSummaries(options: SummaryOptions) {
	return useQuery(traceSummaryQueryOptions(useTransport(), options));
}
