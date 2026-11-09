import { Link } from "react-router";
import type { EnvironmentStatus } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getServiceSpec } from "@/lib/deployment-utils";
import { cn } from "@/lib/utils";

import { METRIC_RANGES, type MetricRange } from "./metric";
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
			<div
				className="h-2 overflow-hidden rounded-full bg-line2"
				role="img"
				aria-label={`${ready.toString()} of ${desired.toString()} replicas ready in ${region.name}`}
			>
				<div
					className={cn("h-full rounded-full", ready < desired ? "bg-warn" : "bg-ok-glyph")}
					style={{ width: `${pct.toString()}%` }}
				/>
			</div>
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
	const memory = useMetricBand({ ...target, metricName: MEMORY_METRIC });

	return (
		<section
			aria-labelledby="replicas-title"
			className="flex flex-col gap-4 rounded-[10px] bg-panel px-5 pt-[18px] pb-4"
		>
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="flex flex-wrap items-baseline gap-2.5">
					<h2 id="replicas-title" className="m-0 text-[16px] font-medium tracking-[-0.01em]">
						Replicas
					</h2>
					<span className="text-[12.5px] text-fg3">
						{replicaLine(environment, nowMs)} · {scaleText(deployed)}
					</span>
				</div>
				<div className="flex flex-wrap items-center gap-3">
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
				</div>
			</div>
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
				/>
				<MetricChart
					title={selected === null ? "Memory" : `Memory · ${selected}`}
					points={memory.points}
					isLoading={memory.isLoading}
					failed={memory.error !== null}
					from={memory.from}
					to={memory.to}
					markers={markers}
				/>
			</div>
		</section>
	);
}
