import { useQuery } from "@connectrpc/connect-query";
import { keepPreviousData } from "@tanstack/react-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Section } from "@/components/design/Page";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { limitSteps, type LimitStep, type Sizing } from "@/lib/limit-history";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { msToTimestamp } from "@/lib/time";

import { RANGE_MS, sharedLimit, type MetricRange } from "./metric";
import { RangeToggle } from "./RangeToggle";
import { RegionAverages } from "./RegionAverages";
import { isDeployed, REFRESH_MS, type RegionView } from "./model";
import { ReplicaCells } from "./ReplicaCells";
import { useDeploymentMarkers } from "./useDeploymentMarkers";

const CPU_METRIC = "k8s.pod.cpu_request_utilization";
const MEMORY_METRIC = "k8s.pod.memory.working_set_limit_utilization";
const WINDOW_DEPLOYMENTS = 200;

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

/** CPU and memory per region over the range, and a cell per replica under them. */
export function MetricsPanel({
	workspaceId,
	environmentId,
	resourceId,
	regions,
	range,
	onRange,
	replicaLogsHref,
	nowMs,
	pendingName,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	regions: RegionView[];
	range: MetricRange;
	onRange: (range: MetricRange) => void;
	replicaLogsHref: (replica: string) => string | undefined;
	nowMs: number;
	/** The resource's name while its metrics have yet to show up. */
	pendingName: string | undefined;
}) {
	const deployed = regions.filter(isDeployed);
	const since = msToTimestamp(nowMs - RANGE_MS[range]);
	// The deployments created in the chart's window draw its limit steps.
	const windowed =
		useQuery(
			listDeployments,
			{ resourceId, environmentId, since, pageSize: WINDOW_DEPLOYMENTS },
			{ placeholderData: keepPreviousData, refetchInterval: REFRESH_MS },
		).data?.deployments ?? [];
	const markers = useDeploymentMarkers({ workspaceId, environmentId, resourceId, range, nowMs });
	const chart = { regions: deployed.map((r) => r.name), workspaceId, environmentId, resourceId, range, markers };

	return (
		<Section
			title="Metrics"
			bodyClassName="flex flex-col gap-3 px-4 py-3"
			actions={pendingName === undefined ? <RangeToggle range={range} onRange={onRange} /> : undefined}
		>
			{pendingName !== undefined ? (
				<p className="m-0 py-2 text-[13px] text-fg3">
					CPU and memory show up a couple of minutes after {pendingName} first starts.
				</p>
			) : (
				<div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-7">
					<RegionAverages
						{...chart}
						title="CPU"
						metricName={CPU_METRIC}
						limits={regionLimits(deployed, windowed, cpuSize)}
						format={formatCpu}
					/>
					<RegionAverages
						{...chart}
						title="Memory"
						metricName={MEMORY_METRIC}
						limits={regionLimits(deployed, windowed, memorySize)}
						format={formatMemory}
					/>
				</div>
			)}
			<div className="grid min-w-0 grid-cols-[max-content_max-content_minmax(0,1fr)] items-start gap-x-4 gap-y-2 border-t border-line2 pt-3.5">
				{deployed.map((r) => (
					<ReplicaCells
						key={r.name}
						workspaceId={workspaceId}
						environmentId={environmentId}
						resourceId={resourceId}
						region={r}
						nowMs={nowMs}
						usageHidden={pendingName !== undefined}
						replicaLogsHref={replicaLogsHref}
					/>
				))}
			</div>
		</Section>
	);
}
