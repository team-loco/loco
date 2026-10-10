import { ChevronDownIcon } from "lucide-react";
import { DeploymentPhase, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { cn } from "@/lib/utils";

import { CopyId } from "./CopyId";
import { fmtCpu, fmtMem, parseCpuMilli, parseMemMi, shortId } from "./format";
import { MetricChart } from "./MetricChart";
import { depService, regionDotClass, type RegionView } from "./model";
import { METRIC_RANGES, useRegionMetric, type MetricRange } from "./useRegionMetric";

function messageClass(dep: Deployment): string {
	switch (dep.status) {
		case DeploymentPhase.FAILED:
			return "text-bad-fg";
		case DeploymentPhase.DEPLOYING:
			return "text-info-fg";
		case DeploymentPhase.PENDING:
			return "text-info-fg";
		case DeploymentPhase.RUNNING:
			return "text-fg3";
		case DeploymentPhase.SUCCEEDED:
			return "text-fg3";
		case DeploymentPhase.CANCELED:
			return "text-fg3";
		case DeploymentPhase.UNSPECIFIED:
			return "text-fg3";
	}
}

function slotClass(dep: Deployment, i: number): string {
	if (i >= dep.replicas) return "border-dashed border-line2 bg-transparent";
	switch (dep.status) {
		case DeploymentPhase.RUNNING:
			return "border-primary bg-primary";
		case DeploymentPhase.FAILED:
			return "border-bad-fg bg-bad-fg";
		case DeploymentPhase.DEPLOYING:
			return "border-info-fg bg-info-bg";
		case DeploymentPhase.PENDING:
			return "border-info-fg bg-info-bg";
		case DeploymentPhase.SUCCEEDED:
			return "border-line2 bg-bg3";
		case DeploymentPhase.CANCELED:
			return "border-line2 bg-bg3";
		case DeploymentPhase.UNSPECIFIED:
			return "border-line2 bg-bg3";
	}
}

function ReplicasCell({ dep }: { dep: Deployment }) {
	const svc = depService(dep);
	const min = svc?.minReplicas ?? dep.replicas;
	const max = svc?.maxReplicas ?? dep.replicas;
	const trackMax = Math.max(max, dep.replicas, 1);
	const slots = Array.from({ length: Math.min(trackMax, 16) }, (_, i) => i);
	const running = dep.status === DeploymentPhase.RUNNING;
	const scalers = svc?.scalers;
	const scalingLine =
		scalers?.enabled === true
			? scalers.cpuTarget !== undefined
				? `Autoscaling on CPU at ${scalers.cpuTarget.toString()}%`
				: `Autoscaling on memory at ${(scalers.memoryTarget ?? 0).toString()}%`
			: "Autoscaling off";
	const cpu = svc?.cpu ?? "—";
	const mem = svc?.memory ?? "—";
	return (
		<div className="flex flex-col gap-2 border-line px-4 py-3.5 lg:border-r">
			<span className="text-sm text-fg3">Replicas</span>
			<div className="flex items-baseline gap-1.5">
				<span className="text-[22px] leading-none font-semibold tabular-nums">{dep.replicas}</span>
				<span className="text-fg3">
					{running ? `/ ${dep.replicas.toString()} healthy` : "desired"}
				</span>
			</div>
			<div className="mt-1 flex gap-[3px]">
				{slots.map((i) => (
					<span key={i} className={cn("h-2.5 flex-1 rounded-[2px] border", slotClass(dep, i))} />
				))}
			</div>
			<div className="flex justify-between text-xs text-fg3">
				<span>{min === max ? `fixed at ${max.toString()}` : `min ${min.toString()}`}</span>
				<span>{min === max ? "" : `max ${max.toString()}`}</span>
			</div>
			<div className="mt-1 flex flex-col gap-[3px] text-sm">
				<span className="text-fg2">{scalingLine}</span>
				<span className="text-fg3">
					{cpu} CPU · {mem} per replica
				</span>
			</div>
		</div>
	);
}

function Charts({
	workspaceId,
	resourceId,
	region,
	range,
	dep,
}: {
	workspaceId: string;
	resourceId: string;
	region: string;
	range: MetricRange;
	dep: Deployment | undefined;
}) {
	const cpuQ = useRegionMetric({ workspaceId, resourceId, region, range, metricName: "k8s.pod.cpu_request_utilization" });
	const memQ = useRegionMetric({ workspaceId, resourceId, region, range, metricName: "k8s.pod.memory_request_utilization" });
	const svc = depService(dep);
	const replicas = dep?.replicas ?? 0;
	const reqCpu = parseCpuMilli(svc?.cpu ?? "");
	const reqMem = parseMemMi(svc?.memory ?? "");
	const live = Math.max(replicas, 1);
	const lastCpu = cpuQ.points.at(-1)?.pct;
	const lastMem = memQ.points.at(-1)?.pct;
	const cpuNow = lastCpu === undefined || reqCpu === 0 ? "—" : fmtCpu(((reqCpu * lastCpu) / 100) * live);
	const memNow = lastMem === undefined || reqMem === 0 ? "—" : fmtMem(((reqMem * lastMem) / 100) * live);
	const cpuUnit = replicas > 0 && reqCpu > 0 ? `of ${fmtCpu(reqCpu * replicas)} requested` : "";
	const memUnit = replicas > 0 && reqMem > 0 ? `of ${fmtMem(reqMem * replicas)} limit` : "";
	const emptyText = (q: { unavailable: boolean; error: Error | null }) =>
		q.unavailable ? "Metrics unavailable for this region" : q.error !== null ? "Failed to load metrics" : "No data in this range";
	return (
		<>
			<MetricChart
				label="CPU"
				current={cpuNow}
				unit={cpuUnit}
				points={cpuQ.points}
				isLoading={cpuQ.isLoading}
				emptyText={emptyText(cpuQ)}
				limitLine={false}
				from={`${range} ago`}
				last={false}
			/>
			<MetricChart
				label="Memory"
				current={memNow}
				unit={memUnit}
				points={memQ.points}
				isLoading={memQ.isLoading}
				emptyText={emptyText(memQ)}
				limitLine
				from={`${range} ago`}
				last
			/>
		</>
	);
}

export function RegionPanel({
	workspaceId,
	resourceId,
	regions,
	region,
	onRegion,
	range,
	onRange,
}: {
	workspaceId: string;
	resourceId: string;
	regions: RegionView[];
	region: RegionView;
	onRegion: (name: string) => void;
	range: MetricRange;
	onRange: (range: MetricRange) => void;
}) {
	const multi = regions.length > 1;
	const dep = region.current;
	return (
		<section className="min-w-0 rounded-lg border border-line bg-background">
			<div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2.5">
				<DropdownMenu>
					<DropdownMenuTrigger render={<Button variant="outline" className="h-[30px] gap-2 px-2.5 font-medium" />}>
						<span className={cn("size-[7px] rounded-full", regionDotClass(region.config?.status))} />
						{region.name}
						{multi && region.primary && <span className="text-xs font-normal text-fg3">primary</span>}
						<ChevronDownIcon className="size-[13px] text-fg3" />
					</DropdownMenuTrigger>
					<DropdownMenuContent className="w-auto min-w-[260px]">
						<DropdownMenuRadioGroup
							value={region.name}
							onValueChange={(v: string) => {
								onRegion(v);
							}}
						>
							{regions.map((r) => (
								<DropdownMenuRadioItem key={r.name} value={r.name} className="gap-2">
									<span className={cn("size-[7px] rounded-full", regionDotClass(r.config?.status))} />
									<span className="flex-1">{r.name}</span>
									{multi && r.primary && <span className="text-xs text-fg3">primary</span>}
									<span className="text-sm text-fg3 tabular-nums">
										{r.current === undefined ? "—" : `${r.current.replicas.toString()} replicas`}
									</span>
								</DropdownMenuRadioItem>
							))}
						</DropdownMenuRadioGroup>
					</DropdownMenuContent>
				</DropdownMenu>
				<div className="flex-1" />
				<ToggleGroup
					variant="segmented"
					value={[range]}
					onValueChange={(v: string[]) => {
						const next = METRIC_RANGES.find((r) => r === v[0]);
						if (next !== undefined) onRange(next);
					}}
				>
					{METRIC_RANGES.map((r) => (
						<ToggleGroupItem key={r} value={r} className="h-6! px-2.5 text-sm">
							{r}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
			</div>
			{dep === undefined ? (
				<div className="px-4 py-8 text-center text-fg3">No deployments in {region.name} yet.</div>
			) : (
				<>
					<div className="flex min-w-0 items-center gap-2.5 border-b border-line px-4 py-2.5">
						<DeploymentPhaseBadge phase={dep.status} />
						<CopyId value={dep.id} className="shrink-0 font-semibold text-foreground">
							{shortId(dep.id)}
						</CopyId>
						<span className={cn("min-w-0 flex-1 wrap-break-word", messageClass(dep))}>{dep.message}</span>
					</div>
					<div className="grid grid-cols-1 lg:grid-cols-[minmax(220px,0.8fr)_minmax(0,1fr)_minmax(0,1fr)]">
						<ReplicasCell dep={dep} />
						<Charts workspaceId={workspaceId} resourceId={resourceId} region={region.name} range={range} dep={dep} />
					</div>
				</>
			)}
		</section>
	);
}
