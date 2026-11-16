import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";

export function useObservabilityMaxRange(): number | undefined {
	const { data, error } = useQuery(
		getConfig,
		{},
		{
			refetchOnMount: (query) => (query.state.data?.observabilityMaxTimeRange === undefined ? "always" : true),
		},
	);
	if (error !== null) throw error;
	const range = data?.observabilityMaxTimeRange;
	if (range === undefined) return undefined;
	return Number(range.seconds) * 1000 + range.nanos / 1_000_000;
}
