import { useTransport } from "@connectrpc/connect-query";
import type { Replica } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";
import { useQueries } from "@tanstack/react-query";
import { ScrollTextIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";

import type { BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatMemory } from "@/lib/quantity";
import { agoLabel, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { replicaState } from "./deployment-progress";
import { shortName } from "./format";
import type { RegionView } from "./model";
import { podUsage, regionReady, replicaLimits, restartCount } from "./replicas";

const USAGE_SPAN_MS = 5 * 60_000;
const USAGE_METRICS = ["k8s.pod.cpu_limit_utilization", "k8s.pod.memory.working_set_limit_utilization"] as const;
const HIGH_SHARE = 0.85;
const MILLI_PER_CORE = 1000;
const MI_PER_GI = 1024;

type CellKind = "ok" | "warn" | "start" | "idle";

const CELL: Record<CellKind, string> = {
	ok: "bg-[color-mix(in_oklch,var(--ok)_38%,var(--bg))]",
	warn: "bg-warn",
	start:
		"bg-[repeating-linear-gradient(135deg,color-mix(in_oklch,var(--info)_30%,var(--bg))_0_2px,var(--bg)_2px_4px)] shadow-[inset_0_0_0_1px_color-mix(in_oklch,var(--info)_70%,var(--bg))]",
	idle: "bg-line2",
};

const KIND: Record<BadgeTone, CellKind> = {
	ok: "ok",
	info: "start",
	warn: "warn",
	bad: "warn",
	neutral: "idle",
	muted: "idle",
	pill: "idle",
};

/** A limit in whole units without trailing zeros. */
function limitUnits(value: number): string {
	return Number(value.toFixed(3)).toString();
}

/** CPU usage against its limit, such as "0.18/1 vCPU". */
function cpuText(ratio: number | undefined, limitMilli: number): string {
	if (limitMilli <= 0 || ratio === undefined) return "—";
	const cores = limitMilli / MILLI_PER_CORE;
	return `${(ratio * cores).toFixed(2)}/${limitUnits(cores)} vCPU`;
}

/** Memory usage against its limit, such as "0.31/2 Gi" or "96/256 Mi". */
function memoryText(ratio: number | undefined, limitMi: number): string {
	if (limitMi <= 0 || ratio === undefined) return "—";
	const used =
		limitMi >= MI_PER_GI ? ((ratio * limitMi) / MI_PER_GI).toFixed(2) : Math.round(ratio * limitMi).toString();
	return `${used}/${formatMemory(limitMi)}`;
}

function ReplicaCell({
	replica,
	region,
	cpu,
	memory,
	logsHref,
	usageHidden,
}: {
	replica: Replica;
	region: RegionView;
	cpu: number | undefined;
	memory: number | undefined;
	logsHref: string | undefined;
	usageHidden: boolean;
}) {
	const [open, setOpen] = useState(false);
	const state = replicaState(replica.state);
	const limit = replicaLimits(region, replica);
	const high = [cpu, memory].some((ratio) => ratio !== undefined && ratio >= HIGH_SHARE);
	const kind = high && state.tone === "ok" ? "warn" : KIND[state.tone];
	const short = shortName(replica.name);
	const status = replica.restarts > 0 ? `${state.label}, ${restartCount(replica)}` : state.label;
	return (
		<Popover open={open} onOpenChange={setOpen}>
			<PopoverTrigger
				openOnHover
				aria-label={`${short}, ${status}`}
				className={cn(
					"size-3 rounded-[3px] border-0 p-0 shadow-[inset_0_0_0_1px_color-mix(in_oklch,var(--fg)_6%,transparent)] hover:ring-1 hover:ring-fg3 aria-expanded:ring-[1.5px] aria-expanded:ring-foreground",
					CELL[kind],
				)}
			/>
			<PopoverContent align="start" className="w-max gap-1.5 rounded-[10px] py-2 pr-2.5 pl-3 text-[12.5px]">
				<div className="flex items-center gap-2">
					<span className="font-mono text-[13px]">{short}</span>
					<span className="text-fg3">{status}</span>
					<span className="flex-1" />
					{logsHref !== undefined && (
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label="Replica logs"
							nativeButton={false}
							render={<Link to={logsHref} />}
						>
							<ScrollTextIcon />
						</Button>
					)}
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label="Close"
						onClick={() => {
							setOpen(false);
						}}
					>
						<XIcon />
					</Button>
				</div>
				{!usageHidden && (
					<div className="flex items-center gap-4 text-fg3">
						<span>
							CPU <span className="ml-1 font-mono text-[12px] text-foreground">{cpuText(cpu, limit.cpuMilli)}</span>
						</span>
						<span>
							Mem{" "}
							<span className="ml-1 font-mono text-[12px] text-foreground">{memoryText(memory, limit.memoryMi)}</span>
						</span>
					</div>
				)}
			</PopoverContent>
		</Popover>
	);
}

/** One line for a region: its code, how many replicas are ready and a cell per replica. */
export function ReplicaCells({
	workspaceId,
	environmentId,
	resourceId,
	region,
	nowMs,
	usageHidden,
	replicaLogsHref,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: RegionView;
	nowMs: number;
	usageHidden: boolean;
	replicaLogsHref: (replica: string) => string | undefined;
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
	const replicas = region.status?.replicas ?? [];
	const stale = region.status?.status === ResourceStatus.UNKNOWN;
	const reportedMs = maybeTsMs(region.status?.reportedAt);
	const { ready, desired } = regionReady(region);
	return (
		<div role="group" aria-label={`Replicas in ${region.name}`} className="contents">
			<span className="font-mono text-[12px] leading-4 whitespace-nowrap text-fg2">{region.name}</span>
			<span className="text-[12px] leading-4 whitespace-nowrap text-fg3">{`${ready.toString()}/${desired.toString()} ready`}</span>
			<span className={cn("flex min-w-0 flex-wrap items-center gap-1 pt-0.5", stale && "opacity-60")}>
				{replicas.map((replica) => (
					<ReplicaCell
						key={replica.uid}
						replica={replica}
						region={region}
						cpu={cpuUsage.get(replica.name)}
						memory={memoryUsage.get(replica.name)}
						logsHref={replicaLogsHref(replica.name)}
						usageHidden={usageHidden}
					/>
				))}
				{replicas.length === 0 && <span className="text-[12px] text-fg3">No replicas reported</span>}
				{stale && (
					<span className="ml-1 text-[12px] text-fg3">
						{reportedMs === undefined ? "Not reported yet" : `Last reported ${agoLabel(reportedMs, nowMs)}`}
					</span>
				)}
			</span>
		</div>
	);
}
