import { Link } from "react-router";
import type { EnvironmentStatus } from "@gen/loco/resource/v1/resource_pb";

import { Progress } from "@/components/design/Progress";
import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { SoonTag } from "@/components/design/SoonTag";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getServiceSpec } from "@/lib/deployment-utils";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { cn } from "@/lib/utils";

import { METRIC_RANGES, sharedLimit, type MetricRange } from "./metric";
import { MetricChart } from "./MetricChart";
import { isDeployed, type RegionView } from "./model";
import { replicaLine } from "./replicas";
import { useMetricBand } from "./useMetricBand";

const UNSUPPORTED_RANGE = "7d";
const PERCENT = 100;
const CPU_METRIC = "k8s.pod.cpu_request_utilization";
const MEMORY_METRIC = "k8s.pod.memory_request_utilization";

function scaleText(regions: RegionView[]): string {
	const pairs = regions.flatMap((r) => {
		const svc = r.current === undefined ? undefined : getServiceSpec(r.current);
		return svc === undefined ? [] : [{ min: svc.minReplicas ?? 0, max: svc.maxReplicas ?? 0 }];
	});
	const first = pairs[0];
	if (first === undefined) return "not deployed";
	if (pairs.some((p) => p.min !== first.min || p.max !== first.max)) return "replicas differ by region";
	const { min, max } = first;
	return min === max ? `fixed at ${min.toString()} per region` : `${min.toString()} to ${max.toString()} per region`;
}

function RegionRow({ region, pressed, onPick }: { region: RegionView; pressed: boolean; onPick: () => void }) {
	const status = region.status;
	const ready = status?.readyReplicas ?? 0;
	const desired = status?.desiredReplicas ?? 0;
	const style = resourceStatusStyle(status?.status);
	const pct = desired === 0 ? 0 : Math.min(PERCENT, (ready / desired) * PERCENT);
	return (
		<div className="grid grid-cols-1 items-center gap-2 sm:grid-cols-[170px_minmax(0,1fr)] sm:gap-3.5">
			<button
				type="button"
				aria-pressed={pressed}
				onClick={onPick}
				className="-ml-2 flex flex-col items-start gap-0.5 rounded-md border-0 bg-transparent px-2 py-1.5 text-left hover:bg-background aria-pressed:bg-background"
			>
				<span className="flex items-center gap-2 font-mono text-[12.5px] text-foreground">
					<StatusGlyph glyph={style.glyph} />
					{region.name}
				</span>
				<span className="text-[11.5px] text-fg3">
					{status === undefined ? "Not reporting" : `${ready.toString()}/${desired.toString()} ready · ${style.label}`}
				</span>
			</button>
			<Progress
				value={pct}
				aria-label={`Replicas ready in ${region.name}`}
				getAriaValueText={() => `${ready.toString()} of ${desired.toString()} replicas ready`}
				className={cn(ready < desired && "[&_[data-slot=progress-indicator]]:bg-warn")}
			/>
		</div>
	);
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
	onRange,
	markers,
	metricsHref,
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
	onRange: (range: MetricRange) => void;
	markers: number[];
	metricsHref: string | undefined;
	nowMs: number;
}) {
	const deployed = regions.filter(isDeployed);
	const region = selected ?? "";
	const target = { workspaceId, environmentId, resourceId, region, range };
	const cpu = useMetricBand({ ...target, metricName: CPU_METRIC });
	const charted = deployed.filter((r) => selected === null || r.name === selected);
	const specs = charted.map((r) => (r.current === undefined ? undefined : getServiceSpec(r.current)));
	const cpuLimit = sharedLimit(specs.map((spec) => parseCpuMilli(spec?.cpu ?? "")));
	const memoryLimit = sharedLimit(specs.map((spec) => parseMemMi(spec?.memory ?? "")));
	const replicas = charted.reduce((n, r) => n + (r.status?.desiredReplicas ?? 0), 0);
	const memory = useMetricBand({ ...target, metricName: MEMORY_METRIC });

	return (
		<Section
			title="Replicas"
			meta={`${replicaLine(environment, nowMs)} · ${scaleText(deployed)}`}
			bodyClassName="flex flex-col gap-4 px-5 pt-[18px] pb-4"
			actions={
				<>
					{selected !== null && (
						<Button
							variant="link"
							onClick={() => {
								onSelect(null);
							}}
						>
							Show all regions
						</Button>
					)}
					{metricsHref !== undefined && (
						<Button variant="link" nativeButton={false} render={<Link to={metricsHref} />}>
							All metrics
						</Button>
					)}
					<ToggleGroup
						variant="segmented"
						aria-label="Time range"
						value={[range]}
						onValueChange={(v: string[]) => {
							const next = METRIC_RANGES.find((r) => r === v[0]);
							if (next !== undefined) onRange(next);
						}}
					>
						{METRIC_RANGES.map((r) => (
							<ToggleGroupItem key={r} value={r}>
								{r}
							</ToggleGroupItem>
						))}
						<ToggleGroupItem value={UNSUPPORTED_RANGE} disabled>
							{UNSUPPORTED_RANGE}
							<SoonTag />
						</ToggleGroupItem>
					</ToggleGroup>
				</>
			}
		>
			<div className="flex flex-col gap-2">
				{deployed.map((r) => (
					<RegionRow
						key={r.name}
						region={r}
						pressed={selected === r.name}
						onPick={() => {
							onSelect(selected === r.name ? null : r.name);
						}}
					/>
				))}
			</div>
			<div className="flex items-center gap-2 rounded-md border border-dashed border-line2 px-3 py-2.5 text-[12.5px] text-fg3">
				Per-replica status, usage and restarts
				<SoonTag />
			</div>
			<div className="grid grid-cols-1 gap-7 border-t border-line2 pt-3.5 lg:grid-cols-2">
				<MetricChart
					title={selected === null ? "CPU" : `CPU · ${selected}`}
					points={cpu.points}
					isLoading={cpu.isLoading}
					failed={cpu.error !== null}
					from={cpu.from}
					to={cpu.to}
					markers={markers}
					limit={cpuLimit}
					format={formatCpu}
					replicas={replicas}
				/>
				<MetricChart
					title={selected === null ? "Memory" : `Memory · ${selected}`}
					points={memory.points}
					isLoading={memory.isLoading}
					failed={memory.error !== null}
					from={memory.from}
					to={memory.to}
					markers={markers}
					limit={memoryLimit}
					format={formatMemory}
					replicas={replicas}
				/>
			</div>
		</Section>
	);
}
