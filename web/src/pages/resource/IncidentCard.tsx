import { useTimePreferences } from "@/lib/use-time-preferences";
import { ScrollTextIcon, Undo2Icon } from "lucide-react";
import { Link } from "react-router";
import type { Deployment, Replica } from "@gen/loco/deployment/v1/deployment_pb";

import type { ReactNode } from "react";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { InlineCode } from "@/components/design/InlineCode";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { aroundWindow, HOUR_MS, windowLabel, windowSpan } from "@/lib/obs-time";
import { placementIn } from "@/lib/deployment-utils";
import { agoLabel, formatClock, formatDateTime, maybeTsMs, msToTimestamp, readyMs, tsMs } from "@/lib/time";

import { LevelBadge } from "@/pages/observability/format";
import { logLevel } from "@/pages/observability/query";

import { replicaState } from "./deployment-progress";
import { NEEDS_WRITE } from "@/hooks/useMyScopes";

import { depTag, type RegionProblem, type RegionView } from "./model";

const LOG_LINES = 3;

function RegionLogs({
	workspaceId,
	environmentId,
	resourceId,
	region,
	query,
	at,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: string;
	query: string;
	at: number | undefined;
	nowMs: number;
}) {
	const timePref = useTimePreferences();
	const window = at === undefined ? { kind: "relative" as const, spanMs: HOUR_MS } : aroundWindow(at, nowMs);
	const logs = useQueryLogs({
		workspaceId,
		environmentId,
		resourceIds: [resourceId],
		spanMs: windowSpan(window),
		query,
		...(window.kind === "absolute"
			? {
					startTime: msToTimestamp(window.from),
					endTime: msToTimestamp(window.to),
				}
			: {}),
		around: at,
		limit: LOG_LINES,
	});
	const lines = logs.entries.toSorted((a, b) => tsMs(a.timestamp) - tsMs(b.timestamp));
	return (
		<div
			aria-label={`Last log lines in ${region}`}
			data-slot="incident-log"
			className="flex flex-col rounded-lg bg-background py-2.5 font-mono text-[11.5px] leading-normal ring-1 ring-line"
		>
			<span className="px-3 pb-1 font-sans text-fg3">{windowLabel(window, timePref)}</span>
			{logs.isLoading && <span className="px-3 text-fg3">Loading…</span>}
			{!logs.isLoading && lines.length === 0 && (
				<span className="px-3 text-fg3">
					{logs.error !== null
						? "Failed to load logs"
						: `No logs ${at === undefined ? "in this region in the last hour" : "around this failure"}`}
				</span>
			)}
			{lines.map((l, i) => {
				const ms = maybeTsMs(l.timestamp);
				return (
					<div
						key={`${String(ms)}-${i.toString()}`}
						className="grid grid-cols-[62px_72px_minmax(0,1fr)] items-center gap-2.5 px-3 py-[3px]"
					>
						<span className="text-fg3">{ms === undefined ? "" : formatClock(ms, false, timePref)}</span>
						<span>
							<LevelBadge level={logLevel(l.level)} />
						</span>
						<span className="truncate" title={l.body}>
							{l.body}
						</span>
					</div>
				);
			})}
		</div>
	);
}

function replicaTitle(replica: Replica): ReactNode {
	const name = <InlineCode>{replica.name}</InlineCode>;
	if (replica.restarts === 0)
		return (
			<>
				Replica {name} is {replicaState(replica.state).label.toLowerCase()}
			</>
		);
	return (
		<>
			Replica {name} restarted {replica.restarts.toString()} {replica.restarts === 1 ? "time" : "times"}
		</>
	);
}

export function IncidentCard({
	problem,
	region,
	lastGood,
	workspaceId,
	environmentId,
	resourceId,
	logsHref,
	canWrite,
	onRollback,
	nowMs,
}: {
	problem: RegionProblem;
	region: RegionView;
	lastGood: Deployment | undefined;
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	/** Logs selected by the reported problem, around its timestamp. */
	logsHref: (query: string, at: number | undefined) => string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	nowMs: number;
}) {
	const timePref = useTimePreferences();
	const current = region.current;
	const placementProblem = placementIn(current, region.name)?.problem;
	const report =
		placementProblem !== undefined &&
		problem.message === [placementProblem.message, placementProblem.detail].filter(Boolean).join(" ")
			? placementProblem
			: undefined;
	const at = maybeTsMs(report?.since) ?? maybeTsMs(region.status?.statusSince);
	const serving = region.serving;
	const failing = (region.status?.replicas ?? []).filter(
		(r) => !r.ready && r.state !== "Stopping" && r.state !== "Stopped",
	);
	const [only] = failing.length === 1 ? failing : [];
	const title = only === undefined ? problem.title : replicaTitle(only);
	const goodMs = lastGood === undefined ? undefined : readyMs(lastGood);
	const servingMs = serving === undefined ? undefined : (maybeTsMs(serving.startedAt) ?? tsMs(serving.createdAt));
	const sub =
		serving === undefined || servingMs === undefined ? (
			(report?.message ?? problem.message)
		) : (
			<>
				Serving deployment <InlineCode>{depTag(serving)}</InlineCode> in <InlineCode>{region.name}</InlineCode>,
				deployed {agoLabel(servingMs, nowMs)}
			</>
		);
	const query = report === undefined || report.logsQuery === "" ? `@region:${region.name}` : report.logsQuery;
	const logs = logsHref(query, at);

	return (
		<section
			aria-labelledby="incident-title"
			className="flex flex-col gap-3.5 rounded-card bg-[color-mix(in_oklch,var(--warn)_6%,transparent)] px-5 pt-[18px] pb-5 ring-1 ring-warn/30 ring-inset"
		>
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="flex min-w-0 flex-col gap-1">
					<h2 id="incident-title" className="m-0 text-[16px] font-medium tracking-[-0.01em]">
						{title}
					</h2>
					{sub !== "" && <span className="text-[13px] text-fg3">{sub}</span>}
				</div>
				{logs !== undefined && (
					<Button size="sm" nativeButton={false} render={<Link to={logs} />}>
						<ScrollTextIcon />
						View replica logs
					</Button>
				)}
			</div>
			{serving !== undefined && report !== undefined && <p className="m-0 text-[13px] text-fg3">{report.message}</p>}
			{report?.detail && (
				<div className="flex min-w-0 flex-wrap gap-x-5 gap-y-2 text-[13px]">
					{report.detail.split(" · ").map((detail, i) => (
						<Badge
							key={i}
							tone="warn"
							className="h-auto max-w-full font-mono [overflow-wrap:anywhere] whitespace-pre-wrap"
						>
							{detail}
						</Badge>
					))}
				</div>
			)}
			{at !== undefined && (
				<span className="text-[12px] text-fg3">Since {formatDateTime(at, true, false, timePref)}</span>
			)}
			<RegionLogs
				workspaceId={workspaceId}
				environmentId={environmentId}
				resourceId={resourceId}
				region={region.name}
				query={query}
				at={at}
				nowMs={nowMs}
			/>
			{lastGood !== undefined && (
				<div className="flex flex-wrap items-center justify-between gap-3 border-t border-warn/20 pt-3">
					<span className="text-[13px]">
						<span className="text-fg3">Last healthy</span>{" "}
						<span className="font-mono text-[12.5px]">{depTag(lastGood)}</span>
						{goodMs !== undefined && `, deployed ${agoLabel(goodMs, nowMs)}`}
					</span>
					<Button
						variant="outline"
						size="sm"
						disabled={!canWrite}
						title={canWrite ? undefined : NEEDS_WRITE}
						onClick={() => {
							onRollback(lastGood, current);
						}}
					>
						<Undo2Icon />
						Roll back
					</Button>
				</div>
			)}
		</section>
	);
}
