import { CheckIcon, ClockIcon, CopyIcon, FileCodeIcon, HashIcon, PackageIcon } from "lucide-react";
import { ResourceStatus, ResourceType, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { PartialBadge } from "@/components/design/PartialBadge";
import { effectiveResourceStatus, ResourceStatusBadge } from "@/components/design/StatusBadge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { useCopy } from "@/hooks/useCopy";
import { useNow } from "@/hooks/useNow";
import { shortImageRef } from "@/lib/image";
import { cn } from "@/lib/utils";

import { CopyId } from "./CopyId";
import { formatDuration, formatStarted, shortId } from "./format";
import { depImage, isRunning, startedMs, type RegionView } from "./model";

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
			return "bg-[#16a34a]";
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

function ImageRef({ image }: { image: string }) {
	const [copiedKey, copy] = useCopy(1500);
	const copied = copiedKey === image;
	const short = shortImageRef(image);

	return (
		<Tooltip>
			<TooltipTrigger
				render={
					<Button
						variant="ghost"
						aria-label={`Copy image ${image}`}
						onClick={() => {
							copy(image, image);
						}}
						className="-mx-1.5 -my-0.5 h-auto max-w-full min-w-0 gap-1.5 px-1.5 py-0.5 font-normal text-fg2 hover:text-foreground"
					/>
				}
			>
				<PackageIcon className="size-3.5 shrink-0 text-fg3" />
				<span className="truncate">{short}</span>
				<span className="flex text-fg3">
					{copied ? <CheckIcon className="size-[13px]" /> : <CopyIcon className="size-[13px]" />}
				</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-[min(90vw,560px)] font-mono break-all">
				{copied ? "Copied" : image}
			</TooltipContent>
		</Tooltip>
	);
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
				{resource.partial !== undefined && <PartialBadge partial={resource.partial} />}
			</div>
			{resource.description !== undefined && resource.description !== "" && (
				<div className="text-fg3">{resource.description}</div>
			)}
			<div className="mt-1.5 flex flex-wrap items-center gap-x-[18px] gap-y-1.5 text-fg2">
				<span className="flex items-center gap-1.5">
					<span className={cn("size-2 rounded-full", healthDotClass(status, desired))} />
					{readyLine}
				</span>
				{prim !== undefined && primImage !== "" && sameImage && <ImageRef image={primImage} />}
				{prim !== undefined && !sameImage && (
					<span className="flex items-center gap-1.5">
						<PackageIcon className="size-3.5 text-fg3" />
						Differs by region
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
