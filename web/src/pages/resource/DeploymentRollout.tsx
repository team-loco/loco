import type { Activity, Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { Link } from "react-router";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Progress } from "@/components/design/Progress";
import { SoonTag } from "@/components/design/SoonTag";
import { useNow } from "@/hooks/useNow";
import { deploymentProblem, placementIn } from "@/lib/deployment-utils";
import { cn, formatShortId } from "@/lib/utils";

import {
	activityTime,
	groupReplicas,
	problemLogsHref,
	PROBLEM_REASONS,
	replicaState,
	stoppingCountdown,
} from "./deployment-progress";
import { depTag } from "./model";

// Keeps the feed scrolled to the newest entry while the reader is at the bottom.
function followActivity(node: HTMLDivElement) {
	let following = true;
	const scroll = () => {
		following = node.scrollHeight - node.clientHeight - node.scrollTop <= 8;
	};
	const follow = () => {
		if (following) node.scrollTop = node.scrollHeight;
	};
	const mutations = new MutationObserver(follow);
	const resize = new ResizeObserver(follow);
	mutations.observe(node, { childList: true, subtree: true, characterData: true });
	resize.observe(node);
	node.addEventListener("scroll", scroll, { passive: true });
	follow();
	return () => {
		mutations.disconnect();
		resize.disconnect();
		node.removeEventListener("scroll", scroll);
	};
}

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

export function ProblemBanner({
	deployment,
	logsHref,
	serving,
	deployTimeoutSeconds,
}: {
	deployment: Deployment;
	logsHref: string | undefined;
	serving: { deployment: Deployment; readyReplicas: number } | undefined;
	deployTimeoutSeconds: number | undefined;
}) {
	const problem = deploymentProblem(deployment);
	if (problem === undefined) return null;
	const problemHref = problemLogsHref(logsHref, deployment);
	const reason = PROBLEM_REASONS[problem.reason];
	const note = problemNote(deployment, problem.reason, deployTimeoutSeconds);
	const servingNote =
		serving === undefined
			? ""
			: `${depTag(serving.deployment)} is still serving on ${serving.readyReplicas.toString()} ${serving.readyReplicas === 1 ? "replica" : "replicas"}.`;
	const sub = [reason === undefined ? "" : problem.message, note, servingNote].filter((part) => part !== "").join(" ");
	const detail = [problem.detail, problem.replicaName].filter((line) => line !== "");
	return (
		<div role="alert" className="flex flex-col gap-2.5">
			<div className="flex min-w-0 flex-col gap-1">
				<span className="text-[14px] font-[550]">{reason ?? problem.message}</span>
				{sub !== "" && <span className="text-[13px] text-fg3">{sub}</span>}
			</div>
			{(detail.length > 0 || problemHref !== undefined) && (
				<div className="relative rounded-lg bg-background py-2.5 pr-28 pl-3 font-mono text-[11.5px] leading-normal ring-1 ring-line">
					{detail.map((line) => (
						<p key={line} className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">
							{line}
						</p>
					))}
					{problemHref !== undefined && (
						<Button
							variant="link"
							nativeButton={false}
							className="absolute top-2.5 right-3.5 font-sans"
							render={<Link to={problemHref} />}
						>
							Open in Logs
						</Button>
					)}
				</div>
			)}
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

export function ActivityFeed({ startMs, activity }: { startMs: number | undefined; activity: readonly Activity[] }) {
	return (
		<section aria-label="Deployment activity" className="min-w-0">
			<div className="mb-2 flex items-center justify-between gap-2">
				<h3 className="m-0 text-[12px] font-medium text-fg3">Activity</h3>
				<span className="flex items-center gap-1.5 text-[12px] text-fg3">
					Replica logs
					<SoonTag />
				</span>
			</div>
			<div
				ref={followActivity}
				// oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The scroll region needs keyboard access.
				tabIndex={0}
				role="region"
				aria-label="Activity feed"
				className="max-h-64 overflow-y-auto overscroll-contain rounded-lg border border-line bg-background p-3 [scrollbar-gutter:stable] focus-visible:outline-2 focus-visible:outline-ring"
			>
				{activity.length === 0 && <p className="m-0 text-[12px] text-fg3">Waiting for activity…</p>}
				<ol className="m-0 flex list-none flex-col gap-1 p-0">
					{activity.map((entry) => (
						<li
							key={String(entry.seq)}
							className="grid min-h-[22px] grid-cols-[3.6rem_minmax(0,1fr)] items-center gap-2.5 text-[12px]"
						>
							<span className="font-mono text-fg3 tabular-nums" title="Time since the rollout started">
								{activityTime(entry.at, startMs)}
								<span className="sr-only"> after the rollout started</span>
							</span>
							<span className="font-mono whitespace-pre-wrap [overflow-wrap:anywhere]">
								{entry.replicaName !== "" && <span className="mr-2 text-fg3">{entry.replicaName}</span>}
								{entry.message}
							</span>
						</li>
					))}
				</ol>
			</div>
		</section>
	);
}
