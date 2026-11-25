import { useTimePreferences } from "@/lib/use-time-preferences";
import { ScrollTextIcon, Undo2Icon } from "lucide-react";
import { Link } from "react-router";
import type { Deployment, Replica } from "@gen/loco/deployment/v1/deployment_pb";

import type { ReactNode } from "react";
import { Button } from "@/components/design/Button";
import { InlineCode } from "@/components/design/InlineCode";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { HOUR_MS } from "@/lib/obs-time";
import { agoLabel, formatClock, maybeTsMs, readyMs, tsMs } from "@/lib/time";

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
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: string;
}) {
	const timePref = useTimePreferences();
	const logs = useQueryLogs({
		workspaceId,
		environmentId,
		resourceIds: [resourceId],
		spanMs: HOUR_MS,
		query: `@region:${region}`,
		limit: LOG_LINES,
	});
	const lines = logs.entries.toReversed();
	return (
		<div
			aria-label={`Last log lines in ${region}`}
			data-slot="incident-log"
			className="flex flex-col rounded-lg bg-background py-2.5 font-mono text-[11.5px] leading-normal ring-1 ring-line"
		>
			{logs.isLoading && <span className="px-3 text-fg3">Loading…</span>}
			{!logs.isLoading && lines.length === 0 && (
				<span className="px-3 text-fg3">
					{logs.error !== null ? "Failed to load logs" : "No logs in this region in the last hour"}
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
	/** Logs of one replica, or of the resource when `replica` is null. */
	logsHref: (replica: string | null) => string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	nowMs: number;
}) {
	const current = region.current;
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
			problem.message
		) : (
			<>
				Serving deployment <InlineCode>{depTag(serving)}</InlineCode> in <InlineCode>{region.name}</InlineCode>,
				deployed {agoLabel(servingMs, nowMs)}
			</>
		);
	const logs = logsHref(only?.name ?? null);

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
			<RegionLogs
				workspaceId={workspaceId}
				environmentId={environmentId}
				resourceId={resourceId}
				region={region.name}
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
