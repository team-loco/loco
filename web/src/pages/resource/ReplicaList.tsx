import { useTransport } from "@connectrpc/connect-query";
import type { Replica } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";
import { useQueries } from "@tanstack/react-query";
import { ChartLineIcon, ScrollTextIcon } from "lucide-react";
import { Link } from "react-router";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Skeleton } from "@/components/design/Skeleton";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { agoLabel, maybeTsMs, shortDurationLabel } from "@/lib/time";
import { cn } from "@/lib/utils";

import { replicaState } from "./deployment-progress";
import { depTag, type RegionView } from "./model";
import { exitLabel, podUsage, regionReady, restartLabel } from "./replicas";

const USAGE_SPAN_MS = 5 * 60_000;
const USAGE_METRICS = ["k8s.pod.cpu_limit_utilization", "k8s.pod.memory.working_set_limit_utilization"] as const;
const PERCENT = 100;
const HIGH_SHARE = 0.85;
/** Regions with more replicas than this show one small cell per replica. */
export const MANY_REPLICAS = 24;

const CELL_TONE: Record<BadgeTone, string> = {
	ok: "bg-ok-glyph",
	info: "bg-info",
	warn: "bg-warn",
	bad: "bg-bad-fg",
	neutral: "bg-line2",
	muted: "bg-line2",
	pill: "bg-line2",
};

function Usage({ name, ratio, limit }: { name: string; ratio: number | undefined; limit: string }) {
	return (
		<span className="text-fg3">
			{name}{" "}
			<span className={cn("text-foreground", ratio !== undefined && ratio >= HIGH_SHARE && "text-warn-fg")}>
				{ratio === undefined ? "—" : `${Math.round(ratio * PERCENT).toString()}%`}
			</span>
			{limit !== "" && ` of ${limit}`}
		</span>
	);
}

/** The replicas the cluster last reported for one region, with each pod's CPU and memory against its limits. */
export function ReplicaList({
	workspaceId,
	environmentId,
	resourceId,
	region,
	many,
	selected,
	onSelect,
	regionPressed,
	onPickRegion,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: RegionView;
	many: boolean;
	regionPressed: boolean;
	onPickRegion: () => void;
	selected: string | null;
	onSelect: (replica: string | null) => void;
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
	const { ready, desired } = regionReady(region);
	const measuring = cpu?.isLoading === true || memory?.isLoading === true;
	const pick = (name: string) => {
		onSelect(selected === name ? null : name);
	};
	return (
		<section aria-label={`Replicas in ${region.name}`} className="flex min-w-0 flex-col gap-2">
			<h3 className="m-0 flex flex-wrap items-baseline gap-x-2 text-[12px] font-normal text-fg3">
				<button
					type="button"
					aria-pressed={regionPressed}
					onClick={onPickRegion}
					className="-ml-1.5 flex items-baseline gap-2 rounded-md border-0 bg-transparent px-1.5 py-0.5 text-left hover:bg-background aria-pressed:bg-background"
				>
					<span className="font-mono text-[12.5px] font-medium text-foreground">{region.name}</span>
					<span>{`${ready.toString()}/${desired.toString()} ready`}</span>
				</button>
				{stale && (
					<span>{reportedMs === undefined ? "Not reported yet" : `Last reported ${agoLabel(reportedMs, nowMs)}`}</span>
				)}
			</h3>
			{replicas.length === 0 && <p className="m-0 text-[12px] text-fg3">No replicas reported</p>}
			{many ? (
				<ul className={cn("m-0 flex list-none flex-wrap gap-1 p-0", stale && "opacity-60")}>
					{replicas.map((replica) => {
						const state = replicaState(replica.state);
						return (
							<li key={replica.uid} className="flex">
								<button
									type="button"
									aria-pressed={selected === replica.name}
									aria-label={`${replica.name}: ${state.label}`}
									title={`${replica.name}: ${state.label}`}
									onClick={() => {
										pick(replica.name);
									}}
									className={cn(
										"size-3.5 rounded-[3px] border-0 p-0 aria-pressed:ring-2 aria-pressed:ring-ring aria-pressed:ring-offset-1",
										CELL_TONE[state.tone],
									)}
								/>
							</li>
						);
					})}
				</ul>
			) : (
				<ul
					className={cn(
						"m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(min(220px,100%),1fr))] gap-2 p-0",
						stale && "opacity-60",
					)}
				>
					{replicas.map((replica) => {
						const state = replicaState(replica.state);
						return (
							<li key={replica.uid} className="flex min-w-0">
								<button
									type="button"
									aria-pressed={selected === replica.name}
									onClick={() => {
										pick(replica.name);
									}}
									className="flex min-w-0 flex-1 flex-col gap-1.5 rounded-lg border border-line bg-background px-3 py-2.5 text-left text-[12px] hover:bg-panel aria-pressed:border-foreground"
								>
									<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
										<span className="min-w-0 break-all font-mono">{replica.name}</span>
										<Badge size="sm" tone={state.tone}>
											{state.label}
										</Badge>
									</span>
									{measuring ? (
										<Skeleton className="h-3.5 w-40" />
									) : (
										<span className="flex min-w-0 flex-wrap gap-x-3 font-mono text-[11.5px] tabular-nums">
											<Usage name="CPU" ratio={cpuUsage.get(replica.name)} limit={cpuLimit} />
											<Usage name="Mem" ratio={memoryUsage.get(replica.name)} limit={memoryLimit} />
										</span>
									)}
								</button>
							</li>
						);
					})}
				</ul>
			)}
		</section>
	);
}

/** Details of the replica the user picked: its state, uptime, restarts, version and links to its telemetry. */
export function ReplicaDetail({
	replica,
	region,
	nowMs,
	metricsHref,
	logsHref,
}: {
	replica: Replica;
	region: RegionView;
	nowMs: number;
	metricsHref: string | undefined;
	logsHref: string | undefined;
}) {
	const state = replicaState(replica.state);
	const startedMs = maybeTsMs(replica.startedAt);
	const deployment = [region.current, ...region.history].find((d) => d?.id === replica.deploymentId);
	return (
		<section
			aria-label={`Replica ${replica.name}`}
			className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg bg-background px-3.5 py-2.5 text-[12.5px]"
		>
			<span className="flex min-w-0 items-center gap-2">
				<span className="break-all font-mono text-[13px]">{replica.name}</span>
				<Badge size="sm" tone={state.tone}>
					{state.label}
				</Badge>
			</span>
			<span className="flex gap-1.5">
				<span className="text-fg3">Up</span>
				<span className="font-mono">{startedMs === undefined ? "—" : shortDurationLabel(nowMs - startedMs)}</span>
			</span>
			<span className="flex gap-1.5">
				<span className="text-fg3">Restarts</span>
				<span className={cn("font-mono", replica.restarts > 0 && "text-warn-fg")}>{restartLabel(replica, nowMs)}</span>
			</span>
			{replica.lastExit !== undefined && replica.lastRestart === undefined && (
				<span className="font-mono text-warn-fg [overflow-wrap:anywhere]">{exitLabel(replica.lastExit, nowMs)}</span>
			)}
			<span className="flex gap-1.5">
				<span className="text-fg3">Version</span>
				<span className="font-mono">{deployment === undefined ? "—" : depTag(deployment)}</span>
			</span>
			<span className="ml-auto flex gap-2">
				{metricsHref !== undefined && (
					<Button variant="outline" size="sm" nativeButton={false} render={<Link to={metricsHref} />}>
						<ChartLineIcon />
						Metrics
					</Button>
				)}
				{logsHref !== undefined && (
					<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
						<ScrollTextIcon />
						Logs
					</Button>
				)}
			</span>
		</section>
	);
}
