import { useQuery } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentPhase, type Deployment, type ServiceDeploymentSpec } from "@gen/loco/deployment/v1/deployment_pb";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { RegionIntentStatus, type RegionConfig, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec } from "@/lib/deployment-utils";

import { imageRef, imageTag, tsMillis } from "./format";

export interface RegionView {
	name: string;
	config: RegionConfig | undefined;
	primary: boolean;
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
	return tsMillis(dep.startedAt) ?? tsMillis(dep.createdAt);
}

export function depService(dep: Deployment | undefined): ServiceDeploymentSpec | undefined {
	return dep === undefined ? undefined : getServiceSpec(dep);
}

export function depImage(dep: Deployment | undefined): string {
	return depService(dep)?.build?.image ?? "";
}

export function depTag(dep: Deployment | undefined): string {
	const image = depImage(dep);
	return image === "" ? "—" : imageTag(image);
}

export function depImageRef(dep: Deployment | undefined): string {
	const image = depImage(dep);
	return image === "" ? "—" : imageRef(image);
}

function byNewest(a: Deployment, b: Deployment): number {
	return (tsMillis(b.createdAt) ?? 0) - (tsMillis(a.createdAt) ?? 0);
}

export function buildRegions(resource: Resource, deployments: Deployment[]): RegionView[] {
	const names = new Set<string>();
	const configured = resource.regions.toSorted((a, b) => Number(b.isPrimary) - Number(a.isPrimary));
	for (const r of configured) names.add(r.region);
	for (const d of deployments) {
		if (d.region !== "") names.add(d.region);
	}
	return [...names].map((name, i) => {
		const config = resource.regions.find((r) => r.region === name);
		const history = deployments.filter((d) => d.region === name).sort(byNewest);
		const current = history.find((d) => d.isActive) ?? history[0];
		return { name, config, primary: config?.isPrimary ?? i === 0, history, current };
	});
}

export function regionDotClass(status: RegionIntentStatus | undefined): string {
	switch (status) {
		case RegionIntentStatus.ACTIVE:
			return "bg-primary";
		case RegionIntentStatus.PROVISIONING:
			return "bg-primary";
		case RegionIntentStatus.DEGRADED:
			return "bg-warn";
		case RegionIntentStatus.FAILED:
			return "bg-err";
		case RegionIntentStatus.REMOVING:
			return "bg-fg4";
		case RegionIntentStatus.DESIRED:
			return "bg-fg4";
		case RegionIntentStatus.UNSPECIFIED:
			return "bg-fg4";
		case undefined:
			return "bg-fg4";
	}
}

export function isRunning(dep: Deployment | undefined): boolean {
	return dep?.status === DeploymentPhase.RUNNING;
}

export function useResourceData(resourceId: string) {
	const resourceQuery = useQuery(
		getResource,
		{ key: { case: "resourceId", value: resourceId } },
		{ enabled: resourceId !== "", refetchInterval: 15_000 },
	);
	const deploymentsQuery = useQuery(
		listDeployments,
		{ resourceId, pageSize: 200 },
		{ enabled: resourceId !== "", refetchInterval: 15_000 },
	);
	const refresh = () => {
		void resourceQuery.refetch();
		void deploymentsQuery.refetch();
	};
	return {
		resource: resourceQuery.data?.resource,
		deployments: deploymentsQuery.data?.deployments ?? [],
		isLoading: resourceQuery.isLoading || deploymentsQuery.isLoading,
		error: resourceQuery.error ?? deploymentsQuery.error,
		refresh,
	};
}
