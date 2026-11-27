import { useTimePreferences } from "@/lib/use-time-preferences";
import { ScrollTextIcon, Undo2Icon } from "lucide-react";
import { Link } from "react-router";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { InlineCode } from "@/components/design/InlineCode";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { aroundWindow, HOUR_MS, windowSpan } from "@/lib/obs-time";
import { placementIn } from "@/lib/deployment-utils";
import { agoLabel, formatClock, formatDateTime, maybeTsMs, msToTimestamp, readyMs, tsMs } from "@/lib/time";

import { LevelBadge } from "@/pages/observability/format";
import { logLevel } from "@/pages/observability/query";

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
			className="flex min-w-0 flex-1 basis-48 flex-col font-mono text-[11.5px] leading-normal"
		>
			{logs.isLoading && <span className="text-fg3">Loading…</span>}
			{!logs.isLoading && lines.length === 0 && (
				<span className="text-fg3">
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
						className="grid grid-cols-[62px_72px_minmax(0,1fr)] items-center gap-2.5 py-[3px]"
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
	const deploymentFailed = report !== undefined && current?.status === DeploymentStatus.FAILED;
	const title = deploymentFailed ? "Deployment failed" : problem.title;
	const diagnostics = (report?.detail ?? "")
		.split(" · ")
		.filter(Boolean)
		.map((detail) => {
			const exit = /^exit (-?\d+)$/.exec(detail);
			if (exit !== null) return { label: "Exit code", value: exit[1] };
			const restarts = /^(\d+) restarts?$/.exec(detail);
			if (restarts !== null) return { label: "Restarts", value: restarts[1] };
			return { label: "Details", value: detail };
		});
	const goodMs = lastGood === undefined ? undefined : readyMs(lastGood);
	const servingMs = serving === undefined ? undefined : (maybeTsMs(serving.startedAt) ?? tsMs(serving.createdAt));
	const explanation = report?.message ?? problem.message;
	const query = report === undefined || report.logsQuery === "" ? `@region:${region.name}` : report.logsQuery;
	const logs = logsHref(query, at);

	return (
		<section
			aria-labelledby="incident-title"
			className="flex flex-col gap-2.5 rounded-card bg-[color-mix(in_oklch,var(--err)_6%,transparent)] px-4 py-3 ring-1 ring-err/30 ring-inset"
		>
			<div className="grid grid-cols-1 items-start gap-2.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:gap-x-4">
				<div className="flex min-w-0 flex-col gap-2.5">
					<div className="flex flex-col gap-1">
						<h2 id="incident-title" className="m-0 text-[14px] font-medium">
							{title}
						</h2>
						{explanation !== "" && <p className="m-0 text-[12.5px] text-fg2">{explanation}</p>}
						{serving !== undefined && servingMs !== undefined && (
							<p className="m-0 text-[12.5px] text-fg2">
								Serving deployment <InlineCode>{depTag(serving)}</InlineCode> in <InlineCode>{region.name}</InlineCode>,
								deployed {agoLabel(servingMs, nowMs)}
							</p>
						)}
					</div>
					<div className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-background px-3 py-2 ring-1 ring-line">
						<RegionLogs
							workspaceId={workspaceId}
							environmentId={environmentId}
							resourceId={resourceId}
							region={region.name}
							query={query}
							at={at}
							nowMs={nowMs}
						/>
						{logs !== undefined && (
							<Button size="sm" nativeButton={false} render={<Link to={logs} />}>
								<ScrollTextIcon />
								View logs
							</Button>
						)}
					</div>
				</div>
				<dl className="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 border-t border-line pt-2.5 text-[12px] text-fg2 sm:max-w-[280px] sm:self-center sm:border-t-0 sm:border-l sm:pt-0 sm:pl-4">
					<dt className="text-fg3">Affected regions</dt>
					<dd className="m-0 font-mono [overflow-wrap:anywhere]">{region.name}</dd>
					{diagnostics.map(({ label, value }, i) => (
						<div key={i} className="contents">
							<dt className="text-fg3">{label}</dt>
							<dd className="m-0 font-mono [overflow-wrap:anywhere] whitespace-pre-wrap">{value}</dd>
						</div>
					))}
					{at !== undefined && (
						<>
							<dt className="text-fg3">Since</dt>
							<dd className="m-0">{formatDateTime(at, false, false, timePref)}</dd>
						</>
					)}
				</dl>
			</div>
			{lastGood !== undefined && (
				<div className="flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
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
