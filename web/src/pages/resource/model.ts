import { createQueryOptions, skipToken, useInfiniteQuery, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { getDeployment, listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentPhase, type Deployment, type ServiceDeploymentSpec } from "@gen/loco/deployment/v1/deployment_pb";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import {
	ResourceStatus,
	type EnvironmentStatus,
	type RegionStatus,
	type Resource,
} from "@gen/loco/resource/v1/resource_pb";

import { useEnvironments } from "@/hooks/useEnvironment";
import { getServiceSpec } from "@/lib/deployment-utils";
import { imageVersion } from "@/lib/image";
import { environmentStatus } from "@/lib/resource-status";
import { maybeTsMs, tsMs } from "@/lib/time";

import { shortId } from "./format";
import { configuredRegions } from "@/lib/resource-regions";

export const PAGE_SIZES = [10, 25, 50, 100];

const DEPLOYMENT_HISTORY_SIZE = 200;
const REFRESH_MS = 15_000;

export interface RegionView {
	name: string;
	status: RegionStatus | undefined;
	history: Deployment[];
	current: Deployment | undefined;
}

export type ModalState =
	| { kind: "spec" }
	| { kind: "diff"; region: string; fromId: string; toId: string }
	| { kind: "rollback"; target: Deployment; current: Deployment | undefined }
	| { kind: "delete" };

export interface Notice {
	tone: "info" | "warn" | "bad";
	title: string;
	message: string;
}

export function startedMs(dep: Deployment): number | undefined {
	return maybeTsMs(dep.startedAt) ?? maybeTsMs(dep.createdAt);
}

export function depService(dep: Deployment | undefined): ServiceDeploymentSpec | undefined {
	return dep === undefined ? undefined : getServiceSpec(dep);
}

export function depImage(dep: Deployment | undefined): string {
	return depService(dep)?.build?.image ?? "";
}

export function depTag(dep: Deployment | undefined): string {
	const image = depImage(dep);
	return image === "" ? "—" : imageVersion(image);
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
		if (d.region !== "") names.add(d.region);
	}
	return [...names].map((name) => {
		const status = environment?.regions.find((r) => r.region === name);
		const history = deployments.filter((d) => d.region === name).toSorted(byNewest);
		const currentId = status?.deploymentId;
		const current = currentId
			? currentDeployments.find((d) => d.id === currentId) ?? history.find((d) => d.id === currentId)
			: history.find((d) => d.isActive) ?? history[0];
		return { name, status, history, current };
	});
}

export function regionDotClass(status: ResourceStatus | undefined): string {
	switch (status) {
		case ResourceStatus.HEALTHY:
			return "bg-ok-fg";
		case ResourceStatus.DEPLOYING:
			return "bg-primary";
		case ResourceStatus.DEGRADED:
			return "bg-warn";
		case ResourceStatus.UNAVAILABLE:
			return "bg-err";
		case ResourceStatus.UNKNOWN:
			return "bg-fg4";
		case ResourceStatus.UNSPECIFIED:
			return "bg-fg4";
		case undefined:
			return "bg-fg4";
	}
}

export interface RegionProblem {
	region: string;
	title: string;
	message: string;
}

export function regionProblem(region: RegionView): RegionProblem | undefined {
	const status = region.status;
	if (status === undefined) return undefined;
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
	if (status.deploymentStatus === DeploymentPhase.FAILED) {
		const title = `Deployment ${shortId(status.deploymentId)} failed in ${region.name}`;
		return { region: region.name, title, message };
	}
	return undefined;
}

export function isRunning(dep: Deployment | undefined): boolean {
	return dep?.status === DeploymentPhase.RUNNING;
}

export function activeDeploymentPlaces(resource: Resource): string[] {
	const environments = resource.environmentStatuses;
	return environments.flatMap((e) =>
		e.regions.map((r) => (environments.length > 1 ? `${r.region} (${e.environmentName})` : r.region)),
	);
}

export function deploymentsRequest(resourceId: string, environmentId: string | undefined) {
	if (resourceId === "" || environmentId === undefined) return skipToken;
	return { resourceId, environmentId, pageSize: DEPLOYMENT_HISTORY_SIZE, pageToken: "" };
}

export function useResourceData(resourceId: string) {
	const transport = useTransport();
	const { active, isLoading: environmentsLoading } = useEnvironments();
	const resourceQuery = useQuery(
		getResource,
		{ key: { case: "resourceId", value: resourceId } },
		{ enabled: resourceId !== "", refetchInterval: REFRESH_MS },
	);
	const deploymentsQuery = useInfiniteQuery(listDeployments, deploymentsRequest(resourceId, active?.id), {
		pageParamKey: "pageToken",
		getNextPageParam: (last) => last.nextPageToken === "" ? undefined : last.nextPageToken,
		refetchInterval: REFRESH_MS,
	});
	const resource = resourceQuery.data?.resource;
	const environment = resource === undefined ? undefined : environmentStatus(resource, active?.id);
	const currentIds = [...new Set(environment?.regions.map((region) => region.deploymentId).filter((id) => id !== ""))];
	const currentQueries = useQueries({
		queries: currentIds.map((deploymentId) => ({
			...createQueryOptions(getDeployment, { deploymentId }, { transport }),
			refetchInterval: REFRESH_MS,
		})),
	});
	const refresh = () => {
		void resourceQuery.refetch();
		void deploymentsQuery.refetch();
		for (const query of currentQueries) void query.refetch();
	};
	const loadMoreHistory = async () => {
		const result = await deploymentsQuery.fetchNextPage();
		return !result.isError;
	};
	return {
		resource,
		environmentId: active?.id,
		deployments: deploymentsQuery.data?.pages.flatMap((page) => page.deployments) ?? [],
		currentDeployments: currentQueries.flatMap((query) => query.data?.deployment === undefined ? [] : [query.data.deployment]),
		hasMoreHistory: deploymentsQuery.hasNextPage,
		isLoadingMoreHistory: deploymentsQuery.isFetchingNextPage,
		historyError: deploymentsQuery.isFetchNextPageError ? deploymentsQuery.error : null,
		loadMoreHistory,
		isLoading: resourceQuery.isLoading || environmentsLoading || deploymentsQuery.isLoading || currentQueries.some((query) => query.isLoading),
		error: resourceQuery.error ?? (deploymentsQuery.isFetchNextPageError ? null : deploymentsQuery.error)
			?? currentQueries.find((query) => query.error !== null)?.error ?? null,
		refresh,
	};
}
