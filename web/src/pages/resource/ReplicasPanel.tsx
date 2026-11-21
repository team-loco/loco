import { useState } from "react";
import type { EnvironmentStatus, RegionStatus } from "@gen/loco/resource/v1/resource_pb";

import { Section } from "@/components/design/Page";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { limitSteps, type LimitStep, type Sizing } from "@/lib/limit-history";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";

import { sharedLimit, type MetricRange } from "./metric";
import { MetricChart } from "./MetricChart";
import { RegionAverages } from "./RegionAverages";
import { isDeployed, type RegionView } from "./model";
import { MANY_REPLICAS, ReplicaDetail, ReplicaList } from "./ReplicaList";
import { regionReady, replicaLine } from "./replicas";
import { useMetricBand } from "./useMetricBand";

const ALL_REGIONS = "all";
const CPU_METRIC = "k8s.pod.cpu_request_utilization";
const MEMORY_METRIC = "k8s.pod.memory.working_set_limit_utilization";

type Size = (target: Sizing) => number;

const cpuSize: Size = (target) => parseCpuMilli(target.cpu ?? "");
const memorySize: Size = (target) => parseMemMi(target.memory ?? "");

function currentTarget(region: RegionView): Sizing | undefined {
	return regionTarget(region.current === undefined ? undefined : getServiceSpec(region.current), region.name);
}

function regionLimits(regions: RegionView[], size: Size): Map<string, LimitStep[]> | undefined {
	const targets = regions.map(currentTarget);
	if (sharedLimit(targets.map((t) => (t === undefined ? 0 : size(t)))) === undefined) return undefined;
	return new Map(
		regions.map((r, i) => {
			const deployments = r.current === undefined ? r.history : [...r.history, r.current];
			return [r.name, limitSteps(deployments, r.name, size, targets[i])];
		}),
	);
}

function scaleText(regions: RegionView[]): string {
	const pairs = regions.flatMap((r) => {
		const svc = regionTarget(r.current === undefined ? undefined : getServiceSpec(r.current), r.name);
		return svc === undefined
			? []
			: [{ min: svc.minReplicas ?? 0, max: svc.maxReplicas ?? 0, cpu: svc.scalers?.cpuTarget }];
	});
	const first = pairs[0];
	if (first === undefined) return "not deployed";
	if (pairs.some((p) => p.min !== first.min || p.max !== first.max || p.cpu !== first.cpu))
		return "scaling differs by region";
	const { min, max, cpu } = first;
	if (min === max) return `fixed at ${min.toString()} per region`;
	return `autoscaling ${min.toString()}–${max.toString()}${cpu === undefined ? "" : `, CPU ${cpu.toString()}%`}`;
}

export function ReplicasPanel({
	workspaceId,
	environmentId,
	resourceId,
	environment,
	regions,
	selected,
	onSelect,
	range,
	markers,
	replicaHref,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	environment: EnvironmentStatus | undefined;
	regions: RegionView[];
	selected: string | null;
	onSelect: (region: string | null) => void;
	range: MetricRange;
	markers: number[];
	replicaHref: (view: "logs" | "metrics", replica: string) => string | undefined;
	nowMs: number;
}) {
	const [picked, setPicked] = useState<string | null>(null);
	const deployed = regions.filter(isDeployed);
	const region = selected ?? "";
	const target = { workspaceId, environmentId, resourceId, region, range };
	const cpu = useMetricBand({ ...target, metricName: CPU_METRIC });
	const charted = deployed.filter((r) => selected === null || r.name === selected);
	const cpuLimits = regionLimits(charted, cpuSize);
	const memoryLimits = regionLimits(charted, memorySize);
	const single = charted.length === 1 ? charted[0]?.name : undefined;
	const memory = useMetricBand({ ...target, metricName: MEMORY_METRIC });
	const pickedRegion = deployed.find((r) => r.name === selected);
	const pickedCount = pickedRegion === undefined ? undefined : regionReady(pickedRegion);
	const selectedLine =
		pickedCount === undefined
			? undefined
			: `${pickedCount.ready.toString()} of ${pickedCount.desired.toString()} ready in ${selected ?? ""}`;
	const countOf = (status: RegionStatus) => {
		const view = regions.find((r) => r.name === status.region);
		return view === undefined ? { ready: 0, desired: status.desiredReplicas } : regionReady(view);
	};
	const compare = selected === null && deployed.length > 1;
	const pickedReplica = charted
		.flatMap((view) => (view.status?.replicas ?? []).map((replica) => ({ region: view, replica })))
		.find((r) => r.replica.name === picked);
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
			meta={`${selectedLine ?? replicaLine(environment, nowMs, countOf)} · ${scaleText(deployed)}`}
			bodyClassName="flex flex-col gap-4 px-5 pt-[18px] pb-4"
			actions={
				<>
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
						many={(r.status?.replicas.length ?? 0) > MANY_REPLICAS}
						selected={picked}
						onSelect={setPicked}
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
							title={selected === null ? "CPU" : `CPU · ${selected}`}
							points={cpu.points}
							isLoading={cpu.isLoading}
							failed={cpu.error !== null}
							from={cpu.from}
							to={cpu.to}
							markers={markers}
							limits={single === undefined ? undefined : cpuLimits?.get(single)}
							format={formatCpu}
						/>
						<MetricChart
							name="Memory"
							title={selected === null ? "Memory" : `Memory · ${selected}`}
							points={memory.points}
							isLoading={memory.isLoading}
							failed={memory.error !== null}
							from={memory.from}
							to={memory.to}
							markers={markers}
							limits={single === undefined ? undefined : memoryLimits?.get(single)}
							format={formatMemory}
						/>
					</>
				)}
			</div>
		</Section>
	);
}
