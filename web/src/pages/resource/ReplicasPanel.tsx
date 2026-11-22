import { useQuery, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQueries } from "@tanstack/react-query";
import { useState } from "react";
import {
	listDeployments,
	queryDeploymentBuckets,
} from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { limitSteps, type LimitStep, type Sizing } from "@/lib/limit-history";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { msToTimestamp } from "@/lib/time";

import { chartWindow, deploymentMarkers, RANGE_MS, replicaChart, sharedLimit, type MetricRange } from "./metric";
import { MetricChart, SERIES, seriesColor } from "./MetricChart";
import { RegionAverages } from "./RegionAverages";
import { isDeployed, REFRESH_MS, type RegionView } from "./model";
import { ReplicaDetail, ReplicaList } from "./ReplicaList";
import { useMetricBand } from "./useMetricBand";

const ALL_REGIONS = "all";
const CPU_METRIC = "k8s.pod.cpu_request_utilization";
const MEMORY_METRIC = "k8s.pod.memory.working_set_limit_utilization";
const MARKER_BUCKETS = 200;
const WINDOW_DEPLOYMENTS = 200;
// nowMs ticks once a minute; the window runs a minute past it to cover the charts' newer end.
const MARKER_LEAD_MS = 60_000;

type Size = (target: Sizing) => number;

const cpuSize: Size = (target) => parseCpuMilli(target.cpu ?? "");
const memorySize: Size = (target) => parseMemMi(target.memory ?? "");

function currentTarget(region: RegionView): Sizing | undefined {
	return regionTarget(region.current === undefined ? undefined : getServiceSpec(region.current), region.name);
}

function regionLimits(
	regions: RegionView[],
	charted: readonly Deployment[],
	size: Size,
): Map<string, LimitStep[]> | undefined {
	const targets = regions.map(currentTarget);
	if (sharedLimit(targets.map((t) => (t === undefined ? 0 : size(t)))) === undefined) return undefined;
	return new Map(
		regions.map((r, i) => {
			const deployments = r.current === undefined ? charted : [...charted, r.current];
			return [r.name, limitSteps(deployments, r.name, size, targets[i])];
		}),
	);
}

export function ReplicasPanel({
	workspaceId,
	environmentId,
	resourceId,
	regions,
	selected,
	onSelect,
	range,
	replicaHref,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	regions: RegionView[];
	selected: string | null;
	onSelect: (region: string | null) => void;
	range: MetricRange;
	replicaHref: (view: "logs" | "metrics", replica: string) => string | undefined;
	nowMs: number;
}) {
	const [picked, setPicked] = useState<string | null>(null);
	const [hovered, setHovered] = useState<string | null>(null);
	const deployed = regions.filter(isDeployed);
	const region = selected ?? "";
	const target = { workspaceId, environmentId, resourceId, region, range };
	const cpu = useMetricBand({ ...target, metricName: CPU_METRIC });
	const charted = deployed.filter((r) => selected === null || r.name === selected);
	const since = msToTimestamp(nowMs - RANGE_MS[range]);
	// The deployments created in the chart's window draw its limit steps.
	const windowed =
		useQuery(
			listDeployments,
			{ resourceId, environmentId, since, pageSize: WINDOW_DEPLOYMENTS },
			{ placeholderData: keepPreviousData, refetchInterval: REFRESH_MS },
		).data?.deployments ?? [];
	const cpuLimits = regionLimits(charted, windowed, cpuSize);
	const memoryLimits = regionLimits(charted, windowed, memorySize);
	const single = charted.length === 1 ? charted[0]?.name : undefined;
	const memory = useMetricBand({ ...target, metricName: MEMORY_METRIC });
	const buckets = useQuery(
		queryDeploymentBuckets,
		{
			workspaceId,
			environmentId,
			resourceIds: [resourceId],
			since,
			until: msToTimestamp(nowMs + MARKER_LEAD_MS),
			bucketCount: MARKER_BUCKETS,
		},
		{ placeholderData: keepPreviousData, refetchInterval: REFRESH_MS },
	);
	const markers = deploymentMarkers(buckets.data?.buckets ?? []);
	const transport = useTransport();
	const [cpuPods, memoryPods] = useQueries({
		queries: [CPU_METRIC, MEMORY_METRIC].map((metricName) =>
			metricsQueryOptions(transport, {
				workspaceId,
				environmentId,
				resourceIds: [resourceId],
				region: single ?? "",
				spanMs: RANGE_MS[range],
				metricName,
				byPod: true,
				enabled: single !== undefined,
			}),
		),
	});
	const cpuReplicas =
		single === undefined ? undefined : replicaChart(cpuPods?.data ?? [], resourceId, cpuLimits?.get(single));
	const memoryReplicas =
		single === undefined ? undefined : replicaChart(memoryPods?.data ?? [], resourceId, memoryLimits?.get(single));
	const cpuWindow = chartWindow(cpuReplicas !== undefined, cpuPods?.dataUpdatedAt, cpu, range);
	const memoryWindow = chartWindow(memoryReplicas !== undefined, memoryPods?.dataUpdatedAt, memory, range);
	const compare = selected === null && deployed.length > 1;
	const pickedReplica = charted
		.flatMap((view) => (view.status?.replicas ?? []).map((replica) => ({ region: view, replica })))
		.find((r) => r.replica.name === picked);
	const lines = [...new Set([...(cpuReplicas?.replicas ?? []), ...(memoryReplicas?.replicas ?? [])])].toSorted();
	// Swatches name a line only while every line has its own colour.
	const lineColor = (replica: string) => {
		const i = lines.indexOf(replica);
		return i < 0 || lines.length > SERIES ? undefined : seriesColor(i);
	};
	const averages = {
		regions: deployed.map((r) => r.name),
		workspaceId,
		environmentId,
		resourceId,
		range,
	};

	return (
		<Section
			title="Replicas"
			bodyClassName="flex flex-col gap-4 px-5 pt-[18px] pb-4"
			actions={
				<>
					{(picked !== null || selected !== null) && (
						<Button
							variant="plain-link"
							onClick={() => {
								setPicked(null);
								onSelect(null);
							}}
						>
							Show all
						</Button>
					)}
					{deployed.length > 1 && (
						<ToggleGroup
							variant="segmented"
							aria-label="Region"
							value={[selected ?? ALL_REGIONS]}
							onValueChange={(v: string[]) => {
								if (v[0] !== undefined) onSelect(v[0] === ALL_REGIONS ? null : v[0]);
							}}
						>
							<ToggleGroupItem value={ALL_REGIONS}>All regions</ToggleGroupItem>
							{deployed.map((r) => (
								<ToggleGroupItem key={r.name} value={r.name} className="font-mono">
									{r.name}
								</ToggleGroupItem>
							))}
						</ToggleGroup>
					)}
				</>
			}
		>
			<div className="flex flex-col gap-3.5">
				{charted.map((r) => (
					<ReplicaList
						key={r.name}
						workspaceId={workspaceId}
						environmentId={environmentId}
						resourceId={resourceId}
						region={r}
						selected={picked}
						onSelect={setPicked}
						onHover={setHovered}
						lineColor={lineColor}
						regionPressed={selected === r.name}
						onPickRegion={() => {
							onSelect(selected === r.name ? null : r.name);
						}}
						nowMs={nowMs}
					/>
				))}
				{pickedReplica !== undefined && (
					<ReplicaDetail
						replica={pickedReplica.replica}
						region={pickedReplica.region}
						nowMs={nowMs}
						metricsHref={replicaHref("metrics", pickedReplica.replica.name)}
						logsHref={replicaHref("logs", pickedReplica.replica.name)}
					/>
				)}
			</div>
			<div className="grid grid-cols-1 gap-7 border-t border-line2 pt-3.5 lg:grid-cols-2">
				{compare ? (
					<>
						<RegionAverages {...averages} title="CPU" metricName={CPU_METRIC} limits={cpuLimits} format={formatCpu} />
						<RegionAverages
							{...averages}
							title="Memory"
							metricName={MEMORY_METRIC}
							limits={memoryLimits}
							format={formatMemory}
						/>
					</>
				) : (
					<>
						<MetricChart
							name="CPU"
							title="CPU"
							points={cpu.points}
							isLoading={cpu.isLoading}
							failed={cpu.error !== null}
							from={cpuWindow.from}
							to={cpuWindow.to}
							markers={markers}
							limits={single === undefined ? undefined : cpuLimits?.get(single)}
							replicas={cpuReplicas}
							picked={hovered ?? picked}
							lineColor={lineColor}
							format={formatCpu}
						/>
						<MetricChart
							name="Memory"
							title="Memory"
							points={memory.points}
							isLoading={memory.isLoading}
							failed={memory.error !== null}
							from={memoryWindow.from}
							to={memoryWindow.to}
							markers={markers}
							limits={single === undefined ? undefined : memoryLimits?.get(single)}
							replicas={memoryReplicas}
							picked={hovered ?? picked}
							lineColor={lineColor}
							format={formatMemory}
						/>
					</>
				)}
			</div>
		</Section>
	);
}
