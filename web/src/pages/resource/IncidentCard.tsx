import { ScrollTextIcon, Undo2Icon } from "lucide-react";
import { Link } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Event } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { agoLabel, formatClock, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { latestBackOff } from "./events";
import { shortId } from "./format";
import { depTag, startedMs, type RegionProblem, type RegionView } from "./model";
import { specChanges } from "./spec";

const LOG_LINES = 6;
const POD_LABEL = "k8s.pod.name";
const ERROR_LEVEL = "ERROR";
const NO_LEVELS: string[] = [];

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<>
			<dt className="text-fg3">{label}</dt>
			<dd className="m-0 [overflow-wrap:anywhere]">{children}</dd>
		</>
	);
}

function PodLogs({
	workspaceId,
	environmentId,
	resourceId,
	pod,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	pod: string;
}) {
	const logs = useQueryLogs({
		workspaceId,
		environmentId,
		resourceIds: [resourceId],
		timeRange: "1h",
		parsedQuery: { search: "", levels: NO_LEVELS, labels: { [POD_LABEL]: pod } },
		limit: LOG_LINES,
	});
	const lines = logs.entries.toReversed();
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<span className="text-[12px] text-fg3">
				Last lines from <span className="font-mono">{pod}</span>
			</span>
			<div
				aria-label={`Last log lines from ${pod}`}
				className="flex flex-col rounded-lg bg-background py-2.5 font-mono text-[11.5px] leading-normal ring-1 ring-line"
			>
				{logs.isLoading && <span className="px-3 text-fg3">Loading…</span>}
				{!logs.isLoading && lines.length === 0 && (
					<span className="px-3 text-fg3">
						{logs.error !== null ? "Failed to load logs" : "No logs from this replica in the last hour"}
					</span>
				)}
				{lines.map((l, i) => {
					const ms = maybeTsMs(l.timestamp);
					return (
						<div
							key={`${String(ms)}-${i.toString()}`}
							className={cn(
								"grid grid-cols-[62px_44px_minmax(0,1fr)] gap-2.5 px-3 py-0.5",
								l.severity.toUpperCase() === ERROR_LEVEL && "bg-bad-bg",
							)}
						>
							<span className="text-fg3">{ms === undefined ? "" : formatClock(ms)}</span>
							<span className="text-fg3">{l.severity}</span>
							<span className="truncate" title={l.body}>
								{l.body}
							</span>
						</div>
					);
				})}
			</div>
		</div>
	);
}

export function IncidentCard({
	problem,
	region,
	lastGood,
	events,
	eventsUnavailable,
	workspaceId,
	environmentId,
	resourceId,
	logsHref,
	onRollback,
	nowMs,
}: {
	problem: RegionProblem;
	region: RegionView;
	lastGood: Deployment | undefined;
	events: Event[];
	eventsUnavailable: boolean;
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	logsHref: string | undefined;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	nowMs: number;
}) {
	const status = region.status;
	const current = region.current;
	const reportedMs = maybeTsMs(status?.reportedAt);
	const backOff = latestBackOff(events);
	const changes = current !== undefined && lastGood !== undefined ? specChanges(lastGood, current) : [];
	const goodMs = lastGood === undefined ? undefined : startedMs(lastGood);

	return (
		<section
			aria-labelledby="incident-title"
			className="flex flex-col gap-4 rounded-[10px] bg-warn-bg px-5 pt-[18px] pb-5 ring-1 ring-warn/30 ring-inset"
		>
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="flex flex-col gap-0.5">
					<h2 id="incident-title" className="m-0 text-[16px] font-medium tracking-[-0.01em]">
						{problem.title}
					</h2>
					<span className="font-mono text-[12px] text-fg3">
						{reportedMs === undefined ? "not reported yet" : `updated ${agoLabel(reportedMs, nowMs)}`}
					</span>
				</div>
				<div className="flex gap-2">
					{logsHref !== undefined && (
						<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
							<ScrollTextIcon />
							Logs
						</Button>
					)}
					{lastGood !== undefined && (
						<Button
							size="sm"
							onClick={() => {
								onRollback(lastGood, current);
							}}
						>
							<Undo2Icon />
							Roll back to {depTag(lastGood)}
						</Button>
					)}
				</div>
			</div>
			<div className="grid grid-cols-1 items-start gap-5 md:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
				<dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-[18px] gap-y-[9px] text-[13px]">
					<Fact label="Region">
						<span className="font-mono">{region.name}</span>
					</Fact>
					{status !== undefined && (
						<Fact label="Replicas">
							{status.readyReplicas}/{status.desiredReplicas} ready
						</Fact>
					)}
					{current !== undefined && (
						<Fact label="Running">
							<span className="font-mono">
								{depTag(current)} · {shortId(current.id)}
							</span>
						</Fact>
					)}
					{problem.message !== "" && <Fact label="Status">{problem.message}</Fact>}
					{backOff !== undefined && (
						<Fact label="Last warning">
							{backOff.reason}: {backOff.message}
						</Fact>
					)}
					<Fact label="Last good">
						{lastGood === undefined ? (
							"No earlier healthy deployment in this region"
						) : (
							<span className="font-mono">
								{depTag(lastGood)} · {shortId(lastGood.id)}
								{goodMs !== undefined && ` · ${agoLabel(goodMs, nowMs)}`}
							</span>
						)}
					</Fact>
					{changes.length > 0 && (
						<Fact label="Changed since">
							<span className="flex flex-col gap-0.5 font-mono text-[12px]">
								{changes.map((c) => (
									<span key={c.label}>
										{c.label}: <span className="text-bad-fg">{c.from}</span> →{" "}
										<span className="text-ok-fg">{c.to}</span>
									</span>
								))}
							</span>
						</Fact>
					)}
				</dl>
				{backOff !== undefined ? (
					<PodLogs
						workspaceId={workspaceId}
						environmentId={environmentId}
						resourceId={resourceId}
						pod={backOff.podName}
					/>
				) : eventsUnavailable ? (
					<span className="flex items-center gap-2 text-[12.5px] text-fg3">
						Last log lines of the failing replica
						<SoonTag />
					</span>
				) : (
					<span className="text-[12.5px] text-fg3">No recent BackOff event names a replica to show logs for.</span>
				)}
			</div>
		</section>
	);
}
