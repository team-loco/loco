import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { listClusterCapacities, listClusters } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import { getOrg, getOrgLimits, listOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import type { Organization } from "@gen/loco/org/v1/org_pb";
import type { QueryClient } from "@tanstack/react-query";

import { StatusBadge, type StatusStyle } from "@/components/design/StatusBadge";
import { clusterState, regionCounts, STATE_LABEL, type ClusterState } from "@/lib/system";
import { formatDateTime, formatMonthDayYear, maybeTsMs, timeZoneLabel } from "@/lib/time";

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

/** The clusters, their regions and the API version, refreshed every 30 seconds. */
export function useFleet() {
	const clusters = useQuery(listClusters, {}, { refetchInterval: POLL_MS });
	const config = useQuery(getConfig, {});
	const list = clusters.data?.clusters ?? [];
	return {
		clusters: list,
		regions: regionCounts(list),
		apiVersion: config.data?.apiVersion ?? "",
		isLoading: clusters.isLoading,
		error: clusters.error,
		stateOf: clusterState,
	};
}

export async function refreshFleet(queryClient: QueryClient): Promise<void> {
	await queryClient.invalidateQueries({
		queryKey: createConnectQueryKey({ schema: listClusters, cardinality: undefined }),
	});
}

/** Whether the organization is suspended, with the reason on hover. */
export function OrgStateBadge({ org }: { org: Organization }) {
	if (org.suspendedAt === undefined) return <StatusBadge glyph="ok" label="Active" />;
	return (
		<span title={org.suspensionReason}>
			<StatusBadge glyph="warn" label="Suspended" />
		</span>
	);
}

/** A date in UTC. */
export function SystemDate({ ts }: { ts: Timestamp | undefined }) {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return <span className="text-fg3">Unknown</span>;
	return <time dateTime={new Date(ms).toISOString()}>{formatMonthDayYear(ms, UTC)}</time>;
}

/** Reloads what the Organizations pages show after a change to an organization. */
export async function refreshOrgs(queryClient: QueryClient): Promise<void> {
	await Promise.all([
		queryClient.invalidateQueries({ queryKey: createConnectQueryKey({ schema: listOrgs, cardinality: undefined }) }),
		queryClient.invalidateQueries({ queryKey: createConnectQueryKey({ schema: getOrg, cardinality: undefined }) }),
		queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: getOrgLimits, cardinality: undefined }),
		}),
	]);
}

export function useClusterCapacities() {
	const query = useQuery(listClusterCapacities, {}, { refetchInterval: POLL_MS });
	return { query, byCluster: new Map((query.data?.capacities ?? []).map((summary) => [summary.clusterId, summary])) };
}
