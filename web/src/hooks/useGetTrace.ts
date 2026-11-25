import { useQuery, type Query } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { scopeReady } from "./observabilityScope";
import { ObservabilityService, type GetTraceResponse } from "@gen/loco/observability/v1/observability_pb";

export interface TraceScopeOptions {
	workspaceId: string;
	installation?: boolean;
	environmentId: string;
	resourceIds: string[];
	enabled?: boolean;
}

type GetTraceOptions = TraceScopeOptions & { traceId: string };

export function getTraceQueryOptions(transport: Transport, options: GetTraceOptions) {
	const { enabled = true, traceId, ...scope } = options;
	const input = { scope, traceId };
	return {
		queryKey: createConnectQueryKey({
			schema: ObservabilityService.method.getTrace,
			transport,
			cardinality: "finite",
			input,
		}),
		queryFn: async ({ signal }: { signal: AbortSignal }) =>
			await createClient(ObservabilityService, transport).getTrace(input, { signal }),
		enabled: enabled && scopeReady(options) && traceId !== "",
	};
}

export function useGetTrace(
	options: GetTraceOptions & { refetchInterval: (query: Query<GetTraceResponse>) => number | false },
) {
	return useQuery({ ...getTraceQueryOptions(useTransport(), options), refetchInterval: options.refetchInterval });
}
