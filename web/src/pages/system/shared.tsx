import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { listClusters } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import { listRegions } from "@gen/loco/cluster/v1/cluster-RegionAdminService_connectquery";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import type { QueryClient } from "@tanstack/react-query";

import { StatusBadge, type StatusStyle } from "@/components/design/StatusBadge";
import { useNow } from "@/hooks/useNow";
import { clusterState, STATE_LABEL, type ClusterState } from "@/lib/system";
import { formatDateTime, maybeTsMs, timeZoneLabel } from "@/lib/time";

const POLL_MS = 30_000;
const UTC = { zone: "utc", clock: "24" } as const;
const LOCAL = { zone: "local", clock: "24" } as const;

const STATE_GLYPH: Record<ClusterState, StatusStyle["glyph"]> = {
	healthy: "ok",
	degraded: "warn",
	unhealthy: "err",
	"not-reporting": "err",
	"never-reported": "draft",
	paused: "off",
	removed: "off",
};

export function ClusterStateBadge({ state }: { state: ClusterState }) {
	return <StatusBadge glyph={STATE_GLYPH[state]} label={STATE_LABEL[state]} />;
}

/** A time in UTC; the title shows it in the viewer's zone. */
export function SystemTime({ ts }: { ts: Timestamp | undefined }) {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return <span className="text-fg3">Never</span>;
	return (
		<time
			dateTime={new Date(ms).toISOString()}
			title={`${formatDateTime(ms, true, false, LOCAL)} ${timeZoneLabel(ms, LOCAL)}`}
			className="font-mono text-[12.5px]"
		>
			{formatDateTime(ms, true, false, UTC)} UTC
		</time>
	);
}

/** The clusters, the regions and the API version, refreshed every 30 seconds. */
export function useFleet() {
	const clusters = useQuery(listClusters, {}, { refetchInterval: POLL_MS });
	const regions = useQuery(listRegions, {}, { refetchInterval: POLL_MS });
	const config = useQuery(getConfig, {});
	const now = useNow(POLL_MS).getTime();
	const list = clusters.data?.clusters ?? [];
	return {
		clusters: list,
		regions: regions.data?.regions ?? [],
		apiVersion: config.data?.apiVersion ?? "",
		now,
		isLoading: clusters.isLoading,
		error: clusters.error,
		stateOf: (cluster: (typeof list)[number]) => clusterState(cluster, now),
	};
}

export async function refreshFleet(queryClient: QueryClient): Promise<void> {
	await Promise.all([
		queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listClusters, cardinality: undefined }),
		}),
		queryClient.invalidateQueries({ queryKey: createConnectQueryKey({ schema: listRegions, cardinality: undefined }) }),
	]);
}
