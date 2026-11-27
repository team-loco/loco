import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { Link } from "react-router";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Progress } from "@/components/design/Progress";
import { SoonTag } from "@/components/design/SoonTag";
import { useNow } from "@/hooks/useNow";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { deploymentProblem, placementIn } from "@/lib/deployment-utils";
import { aroundWindow, HOUR_MS, windowSpan } from "@/lib/obs-time";
import { formatClock, maybeTsMs, msToTimestamp } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { cn, formatShortId } from "@/lib/utils";
import { LevelBadge } from "@/pages/observability/format";
import { logLevel, type Level } from "@/pages/observability/query";

import {
	groupReplicas,
	problemLogsHref,
	PROBLEM_REASONS,
	replicaState,
	stoppingCountdown,
} from "./deployment-progress";
import { depTag } from "./model";

/** A deadline as "10-minute" or "90-second". */
function deadlineLabel(seconds: number): string {
	return seconds % 60 === 0 ? `${(seconds / 60).toString()}-minute` : `${seconds.toString()}-second`;
}

/** The note after a deployment's problem: what Loco does next. */
function problemNote(deployment: Deployment, reason: string, deployTimeoutSeconds: number | undefined): string {
	switch (deployment.status) {
		case DeploymentStatus.FAILED:
		case DeploymentStatus.CRASHED:
			return reason === "build_failed" ? "Nothing was deployed." : "The rollout stopped.";
		case DeploymentStatus.RUNNING:
		case DeploymentStatus.DEGRADED:
			return "The deployment stays running. This clears once its replicas stop restarting.";
		case DeploymentStatus.BUILDING:
		case DeploymentStatus.DEPLOYING:
			return deployTimeoutSeconds === undefined || deployTimeoutSeconds <= 0
				? "Loco keeps retrying until the rollout deadline."
				: `Loco keeps retrying until the ${deadlineLabel(deployTimeoutSeconds)} rollout deadline.`;
		case DeploymentStatus.CANCELED:
		case DeploymentStatus.SUPERSEDED:
		case DeploymentStatus.UNSPECIFIED:
			return "";
	}
}

const PROBLEM_LOG_LINES = 4;

interface Line {
	ms: number | undefined;
	level: Level;
	message: string;
	/** The replica a line is about. */
	replica?: string;
}

/** The link to the logs, with the replica logs that are not available yet beside it. */
export function LogsLink({ href, className }: { href: string; className?: string }) {
	return (
		<span className={cn("absolute top-2.5 right-3.5 flex items-center gap-3 font-sans", className)}>
			<span className="flex items-center gap-1.5 text-[12px] text-fg3">
				Replica logs
				<SoonTag />
			</span>
			<Button variant="link" nativeButton={false} render={<Link to={href} />}>
				Open in Logs
			</Button>
		</span>
	);
}

/** The latest lines of the problem's logs, or the problem's own detail while there are none. */
function ProblemLog({
	deployment,
	workspaceId,
	query,
	detail,
	replica,
	failed,
	href,
}: {
	deployment: Deployment;
	workspaceId: string;
	query: string;
	detail: string;
	replica: string;
	failed: boolean;
	href: string | undefined;
}) {
	const timePref = useTimePreferences();
	const at = maybeTsMs(deploymentProblem(deployment)?.since);
	const nowMs = useNow().getTime();
	const window = at === undefined ? undefined : aroundWindow(at, nowMs);
	const logs = useQueryLogs({
		workspaceId,
		environmentId: deployment.environmentId,
		resourceIds: [deployment.resourceId],
		spanMs: window === undefined ? HOUR_MS : windowSpan(window),
		...(window?.kind === "absolute"
			? { startTime: msToTimestamp(window.from), endTime: msToTimestamp(window.to) }
			: {}),
		query,
		limit: PROBLEM_LOG_LINES,
		enabled: query !== "",
	});
	const found: Line[] = logs.entries
		.toReversed()
		.map((entry) => ({ ms: maybeTsMs(entry.timestamp), level: logLevel(entry.level), message: entry.body }));
	const lines: Line[] =
		found.length > 0 || detail === ""
			? found
			: [{ ms: undefined, level: failed ? "error" : "warn", message: detail, replica }];
	if (lines.length === 0 && href === undefined) return null;
	return (
		<div
			aria-label="Last log lines"
			className="relative flex min-h-9 flex-col rounded-lg bg-background py-2.5 font-mono text-[11.5px] leading-normal ring-1 ring-line"
		>
			{lines.map((line, i) => (
				<div
					key={`${String(line.ms)}-${i.toString()}`}
					className="grid grid-cols-[62px_72px_minmax(0,1fr)] items-center gap-2.5 px-3 py-[3px]"
				>
					<span className="text-fg3">{line.ms === undefined ? "" : formatClock(line.ms, false, timePref)}</span>
					<span>
						<LevelBadge level={line.level} />
					</span>
					<span className="[overflow-wrap:anywhere] whitespace-pre-wrap">
						<span>{line.message}</span>
						{line.replica !== undefined && line.replica !== "" && <span className="ml-2 text-fg3">{line.replica}</span>}
					</span>
				</div>
			))}
			{href !== undefined && <LogsLink href={href} />}
		</div>
	);
}

export function ProblemBanner({
	deployment,
	workspaceId,
	logsHref,
	serving,
	deployTimeoutSeconds,
}: {
	deployment: Deployment;
	workspaceId: string;
	logsHref: string | undefined;
	serving: { deployment: Deployment; readyReplicas: number } | undefined;
	deployTimeoutSeconds: number | undefined;
}) {
	const problem = deploymentProblem(deployment);
	if (problem === undefined) return null;
	const reason = PROBLEM_REASONS[problem.reason];
	const note = problemNote(deployment, problem.reason, deployTimeoutSeconds);
	const servingNote =
		serving === undefined
			? ""
			: `${depTag(serving.deployment)} is still serving on ${serving.readyReplicas.toString()} ${serving.readyReplicas === 1 ? "replica" : "replicas"}.`;
	const sub = [reason === undefined ? "" : problem.message, note, servingNote].filter((part) => part !== "").join(" ");
	const failed = deployment.status === DeploymentStatus.FAILED || deployment.status === DeploymentStatus.CRASHED;
	return (
		<div role="alert" className="flex flex-col gap-2.5">
			<div className="flex min-w-0 flex-col gap-1">
				<span className="text-[14px] font-[550]">{reason ?? problem.message}</span>
				{sub !== "" && <span className="text-[13px] text-fg3">{sub}</span>}
			</div>
			<ProblemLog
				deployment={deployment}
				workspaceId={workspaceId}
				query={problem.logsQuery}
				detail={problem.detail}
				replica={problem.replicaName}
				failed={failed}
				href={problemLogsHref(logsHref, deployment)}
			/>
		</div>
	);
}

/** Groups with more replicas than this list the first ones and summarise the rest. */
const LISTED_REPLICAS = 8;

const CELL_TONE: Record<BadgeTone, string> = {
	ok: "bg-ok-glyph",
	info: "bg-info",
	warn: "bg-warn",
	bad: "bg-bad-fg",
	neutral: "bg-line2",
	muted: "bg-line2",
	pill: "bg-line2",
};

export function ReplicaGroups({ deployment, region }: { deployment: Deployment; region: string }) {
	const placement = placementIn(deployment, region);
	const replicas = placement?.replicas ?? [];
	const now = useNow(replicas.some((r) => r.state === "Stopping") ? 1000 : undefined).getTime();
	return (
		<div className="grid min-w-0 gap-3">
			{groupReplicas(replicas, placement?.currentRevision ?? 0n, deployment.id)
				.filter((group, i) => i === 0 || group.replicas.length > 0)
				.map((group) => (
					<section key={group.label} aria-label={group.label}>
						<h3 className="mt-0 mb-1.5 text-[12px] font-medium text-fg3">{group.label}</h3>
						{group.replicas.length > LISTED_REPLICAS && (
							<div className="mb-1.5 flex flex-col gap-1.5">
								<ul aria-label={`${group.label} replica states`} className="m-0 flex list-none flex-wrap gap-1 p-0">
									{group.replicas.map((replica) => {
										const state = replicaState(replica.state);
										return (
											<li
												key={replica.uid}
												title={`${replica.name}: ${state.label}`}
												className={cn("size-2.5 rounded-[2px]", CELL_TONE[state.tone])}
											>
												<span className="sr-only">{`${replica.name}: ${state.label}`}</span>
											</li>
										);
									})}
								</ul>
							</div>
						)}
						<ul className="m-0 list-none divide-y divide-line2 p-0">
							{group.replicas.length === 0 && <li className="py-1.5 text-[12px] text-fg3">No replicas</li>}
							{group.replicas.slice(0, LISTED_REPLICAS).map((replica) => {
								const state = replicaState(replica.state);
								const countdown = stoppingCountdown(replica, now);
								return (
									<li
										key={replica.uid}
										className={cn("flex min-w-0 flex-col gap-1.5 py-2", replica.state === "Stopped" && "opacity-50")}
									>
										<div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
											<span className="min-w-0 break-all font-mono text-[12px]">{replica.name}</span>
											{replica.deploymentId !== "" && (
												<CopyId value={replica.deploymentId} className="font-mono text-[11px] text-fg3">
													Deployment {formatShortId(replica.deploymentId)}
												</CopyId>
											)}
											<span className="min-w-0 break-all font-mono text-[11px] text-fg3">
												{replica.node || "Node pending"}
											</span>
											<Badge size="sm" tone={state.tone}>
												{state.label}
											</Badge>
										</div>
										{replica.detail !== "" && (
											<p className="m-0 whitespace-pre-wrap text-[12px] text-fg2 [overflow-wrap:anywhere]">
												{replica.detail}
											</p>
										)}
										{countdown !== undefined && (
											<div className="flex max-w-64 items-center gap-2">
												<Progress
													aria-label={`${replica.name} stopping time remaining`}
													value={countdown.percent ?? null}
													className="flex-1 [&_[data-slot=progress-track]]:h-1 [&_[data-slot=progress-indicator]]:bg-warn [&_[data-slot=progress-indicator]]:motion-reduce:transition-none"
												/>
												<span className="shrink-0 font-mono text-[11px] text-fg3">{countdown.seconds}s left</span>
											</div>
										)}
									</li>
								);
							})}
							{group.replicas.length > LISTED_REPLICAS && (
								<li className="py-1.5 text-[12px] text-fg3">
									{`+${(group.replicas.length - LISTED_REPLICAS).toString()} more`}
								</li>
							)}
						</ul>
					</section>
				))}
		</div>
	);
}
