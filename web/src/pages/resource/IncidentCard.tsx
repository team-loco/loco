import { useTimePreferences } from "@/lib/use-time-preferences";
import { ScrollTextIcon, Undo2Icon } from "lucide-react";
import { Link } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { HOUR_MS } from "@/lib/obs-time";
import { deploymentActor } from "@/lib/deployment-utils";
import { agoLabel, formatClock, maybeTsMs, readyMs, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { logLevel } from "@/pages/observability/query";

import { replicaState } from "./deployment-progress";
import { exitLabel } from "./replicas";
import { NEEDS_WRITE } from "@/hooks/useMyScopes";

import { depTag, type RegionProblem, type RegionView } from "./model";
import { ABSENT, specChanges } from "./spec";

const LOG_LINES = 6;

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<>
			<dt className="text-fg3">{label}</dt>
			<dd className="m-0 [overflow-wrap:anywhere]">{children}</dd>
		</>
	);
}

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
		<div className="flex min-w-0 flex-col gap-1.5">
			<span className="text-[12px] text-fg3">
				Last lines in <span className="font-mono">{region}</span>
			</span>
			<div
				aria-label={`Last log lines in ${region}`}
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
							className={cn(
								"grid grid-cols-[62px_44px_minmax(0,1fr)] gap-2.5 px-3 py-0.5",
								logLevel(l.level) === "error" && "bg-bad-bg",
							)}
						>
							<span className="text-fg3">{ms === undefined ? "" : formatClock(ms, false, timePref)}</span>
							<span
								className={cn(
									"text-fg3",
									logLevel(l.level) === "error" && "text-bad-fg",
									logLevel(l.level) === "warn" && "text-warn-fg",
								)}
							>
								{logLevel(l.level).toUpperCase()}
							</span>
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
	const status = region.status;
	const current = region.current;
	const serving = region.serving;
	const sinceMs = maybeTsMs(status?.statusSince);
	const failing = (status?.replicas ?? []).filter((r) => !r.ready && r.state !== "Stopping" && r.state !== "Stopped");
	const [only] = failing.length === 1 ? failing : [];
	const title =
		only === undefined
			? problem.title
			: `Replica ${only.name} ${only.restarts > 0 ? "keeps restarting" : `is ${replicaState(only.state).label.toLowerCase()}`}`;
	const changes = current !== undefined && lastGood !== undefined ? specChanges(lastGood, current) : [];
	const goodMs = lastGood === undefined ? undefined : readyMs(lastGood);
	const meta = [
		sinceMs === undefined ? "not reported yet" : `since ${agoLabel(sinceMs, nowMs)}`,
		...(only !== undefined && only.restarts > 0
			? [`${only.restarts.toString()} ${only.restarts === 1 ? "restart" : "restarts"}`]
			: []),
	].join(", ");
	const servingActor = serving === undefined ? "" : deploymentActor(serving);
	const logs = logsHref(only?.name ?? null);

	return (
		<section
			aria-labelledby="incident-title"
			className="flex flex-col gap-4 rounded-card bg-[color-mix(in_oklch,var(--warn)_6%,transparent)] px-5 pt-[18px] pb-5 ring-1 ring-warn/30 ring-inset"
		>
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
					<h2 id="incident-title" className="m-0 text-[16px] font-medium tracking-[-0.01em]">
						{title}
					</h2>
					<span className="font-mono text-[12px] text-fg3">{meta}</span>
				</div>
				<div className="flex gap-2">
					{logs !== undefined && (
						<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logs} />}>
							<ScrollTextIcon />
							Logs
						</Button>
					)}
					{lastGood !== undefined && (
						<Button
							size="sm"
							disabled={!canWrite}
							title={canWrite ? undefined : NEEDS_WRITE}
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
				<div className="flex min-w-0 flex-col gap-3">
					{problem.message !== "" && <p className="m-0 text-[13px] text-fg2">{problem.message}</p>}
					<dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-[18px] gap-y-[9px] text-[13px]">
						{only?.lastExit !== undefined && (
							<Fact label="Exit">
								<span className="font-mono">{exitLabel(only.lastExit, nowMs)}</span>
							</Fact>
						)}
						{serving !== undefined && (
							<Fact label="Running">
								<span className="font-mono">{depTag(serving)}</span>
								{`, the deployment serving this region, deployed ${agoLabel(maybeTsMs(serving.startedAt) ?? tsMs(serving.createdAt), nowMs)}`}
								{servingActor !== "" && ` by ${servingActor}`}
							</Fact>
						)}
						{changes.length > 0 && (
							<Fact label="Changed">
								<span className="flex flex-col gap-0.5 font-mono text-[12px]">
									{changes.map((c) => (
										<span key={c.label}>
											{c.label}: <span className="text-bad-fg">{c.from ?? ABSENT}</span> →{" "}
											<span className="text-ok-fg">{c.to ?? ABSENT}</span>
										</span>
									))}
								</span>
							</Fact>
						)}
						<Fact label="Last good">
							{lastGood === undefined ? (
								"No earlier healthy deployment in this region"
							) : (
								<>
									<span className="font-mono">{depTag(lastGood)}</span>
									{goodMs !== undefined && `, deployed ${agoLabel(goodMs, nowMs)}`}
								</>
							)}
						</Fact>
					</dl>
				</div>
				<RegionLogs
					workspaceId={workspaceId}
					environmentId={environmentId}
					resourceId={resourceId}
					region={region.name}
				/>
			</div>
		</section>
	);
}
