import { useQuery } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

export interface TraceScopeOptions {
	workspaceId: string;
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
			schema: ObservabilityProxyService.method.getTrace,
			transport,
			cardinality: "finite",
			input,
		}),
		queryFn: async ({ signal }: { signal: AbortSignal }) =>
			await createClient(ObservabilityProxyService, transport).getTrace(input, { signal }),
		enabled: enabled && options.workspaceId !== "" && options.environmentId !== "" && traceId !== "",
		staleTime: 0,
	};
}

export function useGetTrace(options: GetTraceOptions) {
	return useQuery(getTraceQueryOptions(useTransport(), options));
}
