import { useTransport } from "@connectrpc/connect-query";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";
import { useQueries } from "@tanstack/react-query";

import { Badge } from "@/components/design/Badge";
import { Skeleton } from "@/components/design/Skeleton";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { agoLabel, maybeTsMs, shortDurationLabel } from "@/lib/time";
import { cn } from "@/lib/utils";

import { replicaState } from "./deployment-progress";
import type { RegionView } from "./model";
import { exitLabel, podUsage, restartLabel } from "./replicas";

const USAGE_SPAN_MS = 5 * 60_000;
const USAGE_METRICS = ["k8s.pod.cpu_limit_utilization", "k8s.pod.memory.working_set_limit_utilization"] as const;
const PERCENT = 100;

function usageLabel(name: string, ratio: number | undefined, limit: string): string {
	if (ratio === undefined) return `${name} —`;
	return `${name} ${Math.round(ratio * PERCENT).toString()}%${limit === "" ? "" : ` of ${limit}`}`;
}

/** The replicas the cluster last reported for one region, with each pod's CPU and memory against its limits. */
export function ReplicaList({
	workspaceId,
	environmentId,
	resourceId,
	region,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: RegionView;
	nowMs: number;
}) {
	const transport = useTransport();
	const [cpu, memory] = useQueries({
		queries: USAGE_METRICS.map((metricName) =>
			metricsQueryOptions(transport, {
				workspaceId,
				environmentId,
				resourceIds: [resourceId],
				region: region.name,
				spanMs: USAGE_SPAN_MS,
				metricName,
				byPod: true,
			}),
		),
	});
	const cpuUsage = podUsage(cpu?.data ?? [], nowMs);
	const memoryUsage = podUsage(memory?.data ?? [], nowMs);
	const spec = regionTarget(region.current === undefined ? undefined : getServiceSpec(region.current), region.name);
	const cpuMilli = parseCpuMilli(spec?.cpu ?? "");
	const memoryMi = parseMemMi(spec?.memory ?? "");
	const cpuLimit = cpuMilli > 0 ? formatCpu(cpuMilli) : "";
	const memoryLimit = memoryMi > 0 ? formatMemory(memoryMi) : "";
	const replicas = region.status?.replicas ?? [];
	const stale = region.status?.status === ResourceStatus.UNKNOWN;
	const reportedMs = maybeTsMs(region.status?.reportedAt);
	return (
		<section aria-label={`Replicas in ${region.name}`} className="flex min-w-0 flex-col gap-1.5">
			<h3 className="m-0 flex flex-wrap items-baseline gap-x-2 text-[12px] font-medium text-fg3">
				<span className="font-mono">{region.name}</span>
				{stale && (
					<span className="font-normal">
						{reportedMs === undefined ? "Not reported yet" : `Last reported ${agoLabel(reportedMs, nowMs)}`}
					</span>
				)}
			</h3>
			<ul className={cn("m-0 list-none divide-y divide-line2 p-0", stale && "opacity-60")}>
				{replicas.length === 0 && <li className="py-1.5 text-[12px] text-fg3">No replicas reported</li>}
				{replicas.map((replica) => {
					const state = replicaState(replica.state);
					const startedMs = maybeTsMs(replica.startedAt);
					return (
						<li
							key={replica.uid}
							className="grid min-w-0 gap-x-3 gap-y-1 py-2 text-[12px] sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)]"
						>
							<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
								<span className="min-w-0 break-all font-mono">{replica.name}</span>
								<Badge size="sm" tone={state.tone}>
									{state.label}
								</Badge>
							</span>
							<span className="flex min-w-0 flex-wrap gap-x-2 text-fg2">
								<span>{restartLabel(replica, nowMs)}</span>
								{startedMs !== undefined && <span>Up {shortDurationLabel(nowMs - startedMs)}</span>}
								{replica.lastExit !== undefined && replica.lastRestart === undefined && (
									<span className="font-mono text-warn-fg [overflow-wrap:anywhere]">
										{exitLabel(replica.lastExit, nowMs)}
									</span>
								)}
							</span>
							<span className="flex min-w-0 flex-wrap gap-x-3 font-mono text-fg3 tabular-nums">
								{cpu?.isLoading === true || memory?.isLoading === true ? (
									<Skeleton className="h-3.5 w-40" />
								) : (
									<>
										<span>{usageLabel("CPU", cpuUsage.get(replica.name), cpuLimit)}</span>
										<span>{usageLabel("Memory", memoryUsage.get(replica.name), memoryLimit)}</span>
									</>
								)}
							</span>
						</li>
					);
				})}
			</ul>
		</section>
	);
}
