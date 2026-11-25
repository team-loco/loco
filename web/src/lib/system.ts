import type { Cluster } from "@gen/loco/cluster/v1/cluster_pb";
import { ClusterRole } from "@gen/loco/cluster/v1/cluster_pb";
import { EntityType, Scope, type EntityScope } from "@gen/loco/token/v1/token_pb";

import { maybeTsMs } from "@/lib/time";

export const SYSTEM_PATH = "/system";
export const CLUSTERS_PATH = "/system/clusters";
export const NEW_CLUSTER_PATH = "/system/clusters/new";

export function clusterPath(clusterId: string): string {
	return `${CLUSTERS_PATH}/${clusterId}`;
}

export function inSystem(pathname: string): boolean {
	return pathname === SYSTEM_PATH || pathname.startsWith(`${SYSTEM_PATH}/`);
}

/** What the caller may do on the installation: admins change it, readers only look. */
export type SystemAccess = "none" | "read" | "admin";

export function systemAccess(scopes: readonly EntityScope[]): SystemAccess {
	const held = new Set(scopes.filter((s) => s.entityType === EntityType.SYSTEM).map((s) => s.scope));
	if (held.has(Scope.ADMIN)) return "admin";
	return held.has(Scope.READ) ? "read" : "none";
}

export type ClusterState =
	"unhealthy" | "not-reporting" | "degraded" | "never-reported" | "paused" | "healthy" | "removed";

/** A heartbeat older than this means the agent is not reporting. */
export const NOT_REPORTING_MS = 5 * 60_000;

export function clusterState(cluster: Cluster, nowMs: number): ClusterState {
	if (cluster.removedAt !== undefined) return "removed";
	if (!cluster.isActive) return "paused";
	const beat = maybeTsMs(cluster.lastHeartbeat);
	if (beat === undefined) return "never-reported";
	if (nowMs - beat > NOT_REPORTING_MS) return "not-reporting";
	if (cluster.healthStatus === "healthy") return "healthy";
	return cluster.healthStatus === "degraded" ? "degraded" : "unhealthy";
}

const STATE_ORDER: readonly ClusterState[] = [
	"unhealthy",
	"not-reporting",
	"degraded",
	"never-reported",
	"paused",
	"healthy",
	"removed",
];

export function stateRank(state: ClusterState): number {
	return STATE_ORDER.indexOf(state);
}

export const STATE_LABEL: Record<ClusterState, string> = {
	unhealthy: "Unhealthy",
	"not-reporting": "Not reporting",
	degraded: "Degraded",
	"never-reported": "Never reported",
	paused: "Paused",
	healthy: "Healthy",
	removed: "Removed",
};

export const ROLE_LABEL: Record<ClusterRole, string> = {
	[ClusterRole.UNSPECIFIED]: "Unknown",
	[ClusterRole.APPS]: "Apps",
	[ClusterRole.BUILDS]: "Builds",
	[ClusterRole.ALL]: "Apps and builds",
};

export interface RegionGroup {
	region: string;
	clusters: { cluster: Cluster; state: ClusterState }[];
}

/** Regions with their clusters, the region and cluster with the worst state first. */
export function regionGroups(clusters: readonly Cluster[], nowMs: number): RegionGroup[] {
	const byRegion = new Map<string, RegionGroup>();
	for (const cluster of clusters) {
		const group = byRegion.get(cluster.region) ?? { region: cluster.region, clusters: [] };
		group.clusters.push({ cluster, state: clusterState(cluster, nowMs) });
		byRegion.set(cluster.region, group);
	}
	const groups = [...byRegion.values()].map((group) => ({
		...group,
		clusters: group.clusters.toSorted(
			(a, b) => stateRank(a.state) - stateRank(b.state) || a.cluster.name.localeCompare(b.cluster.name),
		),
	}));
	const worst = (group: RegionGroup) => Math.min(...group.clusters.map((c) => stateRank(c.state)));
	return groups.toSorted((a, b) => worst(a) - worst(b) || a.region.localeCompare(b.region));
}

const SEMVER = /^v?(\d+)\.(\d+)\.(\d+)/;

function semver(version: string): [number, number, number] | undefined {
	const match = SEMVER.exec(version);
	if (match === null) return undefined;
	return [Number(match[1]), Number(match[2]), Number(match[3])];
}

/** Whether the agent runs an older release than the API; false when either is not a release version. */
export function agentBehind(apiVersion: string, agentVersion: string | undefined): boolean {
	const api = semver(apiVersion);
	const agent = agentVersion === undefined ? undefined : semver(agentVersion);
	if (api === undefined || agent === undefined) return false;
	for (const [i, part] of agent.entries()) {
		const target = api[i] ?? 0;
		if (part !== target) return part < target;
	}
	return false;
}

export const JOIN_SECRET_NAME = "loco-agent-join-token";
export const WORKER_NAMESPACE = "loco-system";

/** The commands that hand a join token to a worker cluster and install the `loco-worker` chart. */
export function workerInstallCommand(region: string, token: string): string {
	return [
		`kubectl create secret generic ${JOIN_SECRET_NAME} --namespace ${WORKER_NAMESPACE} --from-literal=join-token=${token}`,
		`helm upgrade --install loco-worker ./charts/loco-worker --namespace ${WORKER_NAMESPACE} --set global.region=${region} --set agent.joinToken.existingSecret=${JOIN_SECRET_NAME}`,
	].join("\n");
}
