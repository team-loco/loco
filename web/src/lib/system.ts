import { SystemRole, type SystemRoleHolder } from "@gen/loco/admin/v1/admin_pb";
import type { Cluster } from "@gen/loco/cluster/v1/cluster_pb";
import { ClusterRole } from "@gen/loco/cluster/v1/cluster_pb";
import { EntityType, Scope, type EntityScope } from "@gen/loco/token/v1/token_pb";
import { matchPath } from "react-router";

export const SYSTEM_PATH = "/system";
export const CLUSTERS_PATH = "/system/clusters";
export const SYSTEM_AUDIT_PATH = "/system/audit";
export const NEW_CLUSTER_PATH = "/system/clusters/new";

export const OPERATORS_PATH = "/system/operators";
export const USERS_PATH = "/system/users";

export function clusterPath(clusterId: string): string {
	return `${CLUSTERS_PATH}/${clusterId}`;
}

/** Whether the pathname is `path` or, unless `end`, below it; matched as React Router matches routes. */
export function atPath(pathname: string, path: string, end = false): boolean {
	return matchPath({ path, end }, pathname) !== null;
}

export function inSystem(pathname: string): boolean {
	return atPath(pathname, SYSTEM_PATH);
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

export function clusterState(cluster: Cluster): ClusterState {
	if (cluster.removedAt !== undefined) return "removed";
	if (!cluster.isActive) return "paused";
	if (cluster.silentSince !== undefined) return "not-reporting";
	if (cluster.lastHeartbeat === undefined) return "never-reported";
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
export function regionGroups(clusters: readonly Cluster[]): RegionGroup[] {
	const byRegion = new Map<string, RegionGroup>();
	for (const cluster of clusters) {
		const group = byRegion.get(cluster.region) ?? { region: cluster.region, clusters: [] };
		group.clusters.push({ cluster, state: clusterState(cluster) });
		byRegion.set(cluster.region, group);
	}
	const groups = [...byRegion.values()].map((group) => ({
		...group,
		clusters: group.clusters.toSorted(
			(a, b) => stateRank(a.state) - stateRank(b.state) || a.cluster.name.localeCompare(b.cluster.name),
		),
	}));
	// Each group is sorted, so its first cluster has the worst state.
	const worst = (group: RegionGroup) => stateRank(group.clusters[0]?.state ?? "removed");
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
export const VALUES_FILE = "values.yaml";

/** The commands that hand a join token to a worker cluster and install the `loco-worker` chart. */
export function workerInstallCommand(region: string, token: string): string {
	return [
		`kubectl create secret generic ${JOIN_SECRET_NAME} --namespace ${WORKER_NAMESPACE} --from-literal=join-token=${token} --dry-run=client -o yaml | kubectl apply -f -`,
		`helm upgrade --install loco-worker ./charts/loco-worker --namespace ${WORKER_NAMESPACE} -f ${VALUES_FILE} --set global.region=${region} --set agent.joinToken.existingSecret=${JOIN_SECRET_NAME}`,
	].join("\n");
}

/** Regions with the number of their clusters that are not removed, as the clusters list reports them. */
export function regionCounts(clusters: readonly Cluster[]): { code: string; count: number }[] {
	const counts = new Map<string, number>();
	for (const cluster of clusters) {
		if (cluster.removedAt === undefined) counts.set(cluster.region, (counts.get(cluster.region) ?? 0) + 1);
	}
	return [...counts].map(([code, count]) => ({ code, count })).toSorted((a, b) => a.code.localeCompare(b.code));
}

export type RegistrationTarget =
	{ case: "region"; value: string } | { case: "newRegion"; value: { country: string; subdivision: string } };

export type TargetKind = "existing" | "new" | "local";

export const LOCAL_REGION = "local-1";
const CODE = /^[a-z0-9]+$/;

/** The region a new cluster registers in, or undefined while the form does not name one. */
export function registrationTarget(
	kind: TargetKind,
	existing: string,
	country: string,
	subdivision: string,
): RegistrationTarget | undefined {
	switch (kind) {
		case "local":
			return { case: "region", value: LOCAL_REGION };
		case "existing":
			return existing === "" ? undefined : { case: "region", value: existing };
		case "new":
			return CODE.test(country) && CODE.test(subdivision)
				? { case: "newRegion", value: { country, subdivision } }
				: undefined;
	}
}

/** The page the user left to open System, in local storage, for the switch back. */
export const SYSTEM_RETURN_KEY = "loco:system-return:v1";

export const SYSTEM_ROLE_LABEL: Record<SystemRole, string> = {
	[SystemRole.UNSPECIFIED]: "None",
	[SystemRole.READ]: "Read",
	[SystemRole.ADMIN]: "Admin",
};

/** The role a holder has: the higher of the configured and the granted role. */
export function effectiveRole(holder: Pick<SystemRoleHolder, "configuredRole" | "grantedRole">): SystemRole {
	return holder.configuredRole > holder.grantedRole ? holder.configuredRole : holder.grantedRole;
}

export function roleSource(holder: Pick<SystemRoleHolder, "configuredRole" | "grantedRole">): string {
	const configured = holder.configuredRole !== SystemRole.UNSPECIFIED;
	const granted = holder.grantedRole !== SystemRole.UNSPECIFIED;
	if (configured && granted) return "Configuration and grant";
	return configured ? "Configuration" : "Grant";
}

export type RoleChange = "read" | "admin" | "revoke";

const CONFIGURED_ADMIN = "This user is a configured admin. Change the API configuration to lower the role.";
const CONFIGURED_ONLY = "This role comes from the API configuration. Remove the email there to take it away.";
const LAST_ADMIN = "This is the last system admin. Make another user an admin first.";

/** Why the API refuses a change to a holder's role, or undefined when it accepts it. */
export function roleChangeBlock(
	holder: SystemRoleHolder,
	change: RoleChange,
	holders: readonly SystemRoleHolder[],
): string | undefined {
	if (change === "admin") return undefined;
	if (change === "revoke" && holder.grantedRole === SystemRole.UNSPECIFIED) return CONFIGURED_ONLY;
	if (change === "read" && holder.configuredRole === SystemRole.ADMIN) return CONFIGURED_ADMIN;
	const admins = holders.filter((h) => effectiveRole(h) === SystemRole.ADMIN).length;
	const keepsConfiguredAdmin = change === "revoke" && holder.configuredRole === SystemRole.ADMIN;
	const removesLastAdmin = effectiveRole(holder) === SystemRole.ADMIN && admins === 1 && !keepsConfiguredAdmin;
	if (removesLastAdmin) return LAST_ADMIN;
	return undefined;
}
