import { ClockIcon, FileCodeIcon, HashIcon, PackageIcon } from "lucide-react";
import { ResourceStatus, ResourceType, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/design/Badge";
import { effectiveResourceStatus, ResourceStatusBadge } from "@/components/design/StatusBadge";
import { useNow } from "@/hooks/useNow";
import { cn } from "@/lib/utils";

import { CopyId } from "./CopyId";
import { formatDuration, formatStarted, shortId } from "./format";
import { depImage, depImageRef, isRunning, startedMs, type RegionView } from "./model";

function typeLabel(type: ResourceType): string {
	switch (type) {
		case ResourceType.SERVICE:
			return "Service";
		case ResourceType.DATABASE:
			return "Database";
		case ResourceType.FUNCTION:
			return "Function";
		case ResourceType.CACHE:
			return "Cache";
		case ResourceType.QUEUE:
			return "Queue";
		case ResourceType.BLOB:
			return "Blob";
		case ResourceType.UNSPECIFIED:
			return "Resource";
	}
}

function healthDotClass(status: ResourceStatus, desired: number): string {
	if (desired === 0) return "bg-fg4";
	switch (status) {
		case ResourceStatus.HEALTHY:
			return "bg-ok-fg";
		case ResourceStatus.DEPLOYING:
			return "bg-warn";
		case ResourceStatus.DEGRADED:
			return "bg-warn";
		case ResourceStatus.UNAVAILABLE:
			return "bg-err";
		case ResourceStatus.SUSPENDED:
			return "bg-fg4";
		case ResourceStatus.UNSPECIFIED:
			return "bg-fg4";
	}
}

export function ResourceHeader({
	resource,
	regions,
	hasDeployments,
	onViewSpec,
}: {
	resource: Resource;
	regions: RegionView[];
	hasDeployments: boolean;
	onViewSpec: () => void;
}) {
	const status = effectiveResourceStatus(resource.status, hasDeployments);
	const now = useNow(60_000);
	const primary = regions.find((r) => r.primary) ?? regions[0];
	const prim = primary?.current;
	const actives = regions.flatMap((r) => (r.current === undefined ? [] : [r.current]));
	const desired = actives.reduce((sum, d) => sum + d.replicas, 0);
	const allRunning = actives.length > 0 && actives.every((d) => isRunning(d));
	const regionSuffix = regions.length > 1 ? ` · ${regions.length.toString()} regions` : "";
	const readyLine =
		actives.length === 0
			? "Not deployed"
			: desired === 0
				? "Scaled to zero"
				: allRunning
					? `${desired.toString()}/${desired.toString()} replicas healthy${regionSuffix}`
					: `${desired.toString()} replicas desired${regionSuffix}`;
	const primImage = depImage(prim);
	const sameImage = actives.every((d) => depImage(d) === primImage);
	const imageLabel = sameImage ? depImageRef(prim) : "Differs by region";
	const since = prim === undefined ? undefined : startedMs(prim);
	const extra = actives.length > 1 ? ` +${(actives.length - 1).toString()}` : "";
	const deployTitle = regions
		.flatMap((r) => (r.current === undefined ? [] : [`${r.name}: ${shortId(r.current.id)}`]))
		.join(" · ");

	return (
		<div className="flex flex-col gap-1.5">
			<div className="flex flex-wrap items-center gap-3">
				<h1 className="m-0 text-2xl font-semibold tracking-[-0.01em]">{resource.name}</h1>
				<ResourceStatusBadge status={status} />
				<Badge tone="outline" size="sm" className="text-sm text-fg3">
					{typeLabel(resource.type)}
				</Badge>
			</div>
			{resource.description !== undefined && resource.description !== "" && (
				<div className="text-fg3">{resource.description}</div>
			)}
			<div className="mt-1.5 flex flex-wrap items-center gap-x-[18px] gap-y-1.5 text-fg2">
				<span className="flex items-center gap-1.5">
					<span className={cn("size-2 rounded-full", healthDotClass(status, desired))} />
					{readyLine}
				</span>
                {resource.stackName !== "" && (
                    <Badge size="sm" className="bg-bg3 text-fg2">
                        Stack {resource.stackName} · {resource.serviceKey}
                    </Badge>
                )}
				{prim !== undefined && (
					<span className="flex items-center gap-1.5" title={primImage}>
						<PackageIcon className="size-3.5 text-fg3" />
						{imageLabel}
					</span>
				)}
				{prim !== undefined && (
					<CopyId value={prim.id} title={deployTitle} className="-mx-1.5 -my-0.5 text-fg2 hover:text-foreground">
						<HashIcon className="size-3.5 text-fg3" />
						{shortId(prim.id)}
						{extra}
					</CopyId>
				)}
				{since !== undefined && (
					<span className="flex items-center gap-1.5" title={`Since ${formatStarted(since)}`}>
						<ClockIcon className="size-3.5 text-fg3" />
						Active {formatDuration(now.getTime() - since)}
					</span>
				)}
				{prim !== undefined && (
					<button
						type="button"
						onClick={onViewSpec}
						className="flex items-center gap-1.5 border-0 bg-transparent p-0 text-link hover:underline"
					>
						<FileCodeIcon className="size-3.5" />
						View spec
					</button>
				)}
			</div>
		</div>
	);
}
