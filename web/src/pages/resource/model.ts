import { formatShortId } from "@/lib/utils";
import { createQueryOptions, skipToken, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { getDeployment, listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentStatus, type Deployment, type ServiceDeploymentSpec } from "@gen/loco/deployment/v1/deployment_pb";
import type { Secret } from "@gen/loco/secret/v1/secret_pb";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import {
	ResourceStatus,
	type EnvironmentStatus,
	type RegionStatus,
	type Resource,
} from "@gen/loco/resource/v1/resource_pb";

import { useEnvironments } from "@/hooks/useEnvironment";
import { needsAttention } from "@/lib/alerts";
import { getServiceSpec, isInFlight, placementIn } from "@/lib/deployment-utils";
import { imageTag } from "@/lib/image";
import { environmentStatus } from "@/lib/resource-status";
import { maybeTsMs, tsMs } from "@/lib/time";

import { configuredRegions } from "@/lib/resource-regions";

export const PAGE_SIZES = [10, 25, 50, 100];
export const DEFAULT_PAGE_SIZE = 10;

export const REFRESH_MS = 15_000;

export interface RegionView {
	name: string;
	status: RegionStatus | undefined;
	history: Deployment[];
	current: Deployment | undefined;
	/** The deployment whose ready replicas serve the region. */
	serving: Deployment | undefined;
	/** The deployment a rollback in this region returns to. */
	lastGood: Deployment | undefined;
}

export type ModalState =
	| { kind: "spec"; deploymentId: string; compareId: string | null; shown: Deployment[] }
	| { kind: "rollback"; target: Deployment; current: Deployment | undefined }
	| { kind: "redeploy" }
	| { kind: "restart" }
	| { kind: "delete" };

export interface Notice {
	tone: "info" | "warn" | "bad";
	title: string;
	message: string;
}

/** The secrets the resource reads that changed after its oldest running replica started, by name. */
export function changedSecrets(
	secrets: readonly Secret[],
	resourceId: string,
	regions: readonly RegionView[],
): string[] {
	const starts = regions.flatMap((r) =>
		(r.status?.replicas ?? []).flatMap((replica) => {
			const ms = maybeTsMs(replica.startedAt);
			return ms === undefined || replica.state === "Stopping" || replica.state === "Stopped" ? [] : [ms];
		}),
	);
	if (starts.length === 0) return [];
	const oldest = Math.min(...starts);
	return secrets
		.filter((s) => s.usedBy.some((u) => u.resourceId === resourceId) && tsMs(s.updatedAt) > oldest)
		.map((s) => s.name)
		.toSorted();
}

function depService(dep: Deployment | undefined): ServiceDeploymentSpec | undefined {
	return dep === undefined ? undefined : getServiceSpec(dep);
}

export function depImage(dep: Deployment | undefined): string {
	return depService(dep)?.build?.image ?? "";
}

export function depTag(dep: Deployment | undefined): string {
	return imageTag(depImage(dep));
}

function byNewest(a: Deployment, b: Deployment): number {
	return tsMs(b.createdAt) - tsMs(a.createdAt);
}

export function buildRegions(
	resource: Resource,
	deployments: Deployment[],
	environment: EnvironmentStatus | undefined,
	currentDeployments: Deployment[] = [],
): RegionView[] {
	const names = new Set<string>();
	for (const r of configuredRegions(resource)) names.add(r);
	for (const r of environment?.regions ?? []) names.add(r.region);
	for (const d of deployments) {
		for (const p of d.placements) {
			if (p.region !== "") names.add(p.region);
		}
	}
	return [...names].map((name) => {
		const status = environment?.regions.find((r) => r.region === name);
		const history = deployments.filter((d) => placementIn(d, name) !== undefined).toSorted(byNewest);
		const byId = (id: string) => currentDeployments.find((d) => d.id === id) ?? history.find((d) => d.id === id);
		const currentId = status?.deploymentId;
		const current = currentId ? byId(currentId) : history.find((d) => placementIn(d, name)?.live);
		const servingId = status?.servingDeploymentId;
		const serving = servingId ? byId(servingId) : undefined;
		const lastGoodId = status?.lastGoodDeploymentId;
		const lastGood = lastGoodId ? byId(lastGoodId) : undefined;
		return { name, status, history, current, serving, lastGood };
	});
}

export interface RegionProblem {
	region: string;
	title: string;
	message: string;
}

export function regionProblem(region: RegionView): RegionProblem | undefined {
	const status = region.status;
	if (status === undefined || !needsAttention(status)) return undefined;
	const message = status.message ?? "";
	switch (status.status) {
		case ResourceStatus.UNAVAILABLE:
			return { region: region.name, title: `${region.name} is down`, message };
		case ResourceStatus.DEGRADED:
			return { region: region.name, title: `${region.name} is degraded`, message };
		case ResourceStatus.UNKNOWN:
			return { region: region.name, title: `${region.name} is not reporting`, message };
		case ResourceStatus.HEALTHY:
			break;
		case ResourceStatus.DEPLOYING:
			break;
		case ResourceStatus.UNSPECIFIED:
			break;
	}
	return {
		region: region.name,
		title: `Deployment ${formatShortId(status.deploymentId)} failed in ${region.name}`,
		message,
	};
}

export function isDeployed(region: RegionView): boolean {
	return placementIn(region.current, region.name)?.live === true;
}

export function canRollBackTo(dep: Deployment, region: RegionView): boolean {
	return (
		dep.id !== region.current?.id &&
		dep.status !== DeploymentStatus.FAILED &&
		dep.status !== DeploymentStatus.CRASHED &&
		dep.spec !== undefined
	);
}

/** A deployment of the region the page has loaded. */
export function regionDeployment(region: RegionView, id: string | undefined): Deployment | undefined {
	if (id === undefined || id === "") return undefined;
	return [region.current, region.serving, region.lastGood, ...region.history].find((d) => d?.id === id);
}

export function problemRegionOf(regions: RegionView[]): RegionView | undefined {
	return regions.find(
		(r) => (r.current === undefined || !isInFlight(r.current.status)) && regionProblem(r) !== undefined,
	);
}

// A healthy region without an earlier release must not hide the rollback a sibling region still offers.
export function focusRegion(regions: RegionView[]): RegionView | undefined {
	return problemRegionOf(regions) ?? regions.find((r) => isDeployed(r) && r.lastGood !== undefined);
}

/** The deployment before dep in the region, by ID; empty for the region's first deployment. */
export function previousDeploymentId(dep: Deployment, region: string): string {
	return placementIn(dep, region)?.previousDeploymentId ?? "";
}

/** How long after a resource first starts its metrics take to show up. */
const METRICS_DELAY_MS = 2 * 60_000;

/** Whether no deployed region has a placement that started METRICS_DELAY_MS or more ago. */
export function metricsPending(deployed: readonly RegionView[], nowMs: number): boolean {
	return (
		deployed.length > 0 &&
		deployed.every((r) =>
			[r.current, ...r.history].every((d) => {
				const startedMs = maybeTsMs(placementIn(d, r.name)?.startedAt);
				return startedMs === undefined || nowMs - startedMs < METRICS_DELAY_MS;
			}),
		)
	);
}

/** Time from creation until the deployment turned ready, or until it failed or was canceled. */
export function tookMs(dep: Deployment): number | undefined {
	const created = maybeTsMs(dep.createdAt);
	const end = maybeTsMs(dep.startedAt) ?? maybeTsMs(dep.completedAt);
	if (created === undefined || end === undefined) return undefined;
	return Math.max(0, end - created);
}

/** The newest page of the environment's deployments, the history table's first page at its default size. */
export function deploymentsRequest(resourceId: string, environmentId: string | undefined) {
	if (resourceId === "" || environmentId === undefined) return skipToken;
	return { resourceId, environmentId, pageSize: DEFAULT_PAGE_SIZE };
}

export function statusEnvironmentId(resource: Resource, requested: string | null): string | undefined {
	const statuses = resource.environmentStatuses;
	const match =
		requested === null
			? statuses[0]
			: statuses.find((e) => e.environmentSlug === requested || e.environmentId === requested);
	return match?.environmentId;
}

export function useResourceData(resourceId: string) {
	const transport = useTransport();
	const { active, requested, isLoading: environmentsLoading } = useEnvironments();
	const resourceQuery = useQuery(
		getResource,
		{ key: { case: "resourceId", value: resourceId } },
		{ enabled: resourceId !== "", refetchInterval: REFRESH_MS },
	);
	const resource = resourceQuery.data?.resource;
	const environmentId =
		active?.id ??
		(resource === undefined || environmentsLoading ? undefined : statusEnvironmentId(resource, requested));
	// The history table's first page shares this request and mounts after it.
	const deploymentsQuery = useQuery(listDeployments, deploymentsRequest(resourceId, environmentId), {
		refetchInterval: REFRESH_MS,
		staleTime: REFRESH_MS,
	});
	const environment = resource === undefined ? undefined : environmentStatus(resource, environmentId);
	const currentIds = [
		...new Set(
			environment?.regions
				.flatMap((region) => [region.deploymentId, region.servingDeploymentId, region.lastGoodDeploymentId])
				.filter((id) => id !== ""),
		),
	];
	const currentQueries = useQueries({
		queries: currentIds.map((deploymentId) => ({
			...createQueryOptions(getDeployment, { deploymentId }, { transport }),
			refetchInterval: REFRESH_MS,
		})),
	});
	const refresh = async () => {
		await Promise.all([
			resourceQuery.refetch(),
			deploymentsQuery.refetch(),
			...currentQueries.map(async (query) => await query.refetch()),
		]);
	};
	return {
		resource,
		environmentId,
		deployments: deploymentsQuery.data?.deployments ?? [],
		currentDeployments: currentQueries.flatMap((query) =>
			query.data?.deployment === undefined ? [] : [query.data.deployment],
		),
		isLoading:
			resourceQuery.isLoading ||
			environmentsLoading ||
			deploymentsQuery.isLoading ||
			currentQueries.some((query) => query.isLoading),
		error:
			resourceQuery.error ??
			deploymentsQuery.error ??
			currentQueries.find((query) => query.error !== null)?.error ??
			null,
		refresh,
	};
}
