import type { Activity, Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { LogsIcon } from "lucide-react";
import { Link } from "react-router";

import { Badge } from "@/components/design/Badge";
import { Banner } from "@/components/design/Banner";
import { Button } from "@/components/design/Button";
import { Progress } from "@/components/design/Progress";
import { useNow } from "@/hooks/useNow";
import { cn } from "@/lib/utils";

import { activityRows, groupReplicas, problemLogsHref, replicaState, stoppingCountdown } from "./deployment-progress";

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

export function DeploymentRollout({
	deployment,
	activity,
	logsHref,
}: {
	deployment: Deployment;
	activity: readonly Activity[];
	logsHref: string | undefined;
}) {
	const now = useNow(deployment.replicas.some((r) => r.state === "Stopping") ? 1000 : undefined).getTime();
	const problem = deployment.problem;
	const problemHref = problemLogsHref(logsHref, deployment);
	const rows = activityRows(activity, deployment.createdAt);
	return (
		<div className="flex min-w-0 flex-col gap-4">
			{problem !== undefined && (
				<Banner
					title={problem.message}
					tone={deployment.status === DeploymentStatus.FAILED ? "bad" : "warn"}
					className="mx-0 mt-0"
				>
					{problem.detail !== "" && (
						<p className="my-1 whitespace-pre-wrap [overflow-wrap:anywhere]">{problem.detail}</p>
					)}
					{problem.replicaName !== "" && <p className="my-1 break-all font-mono text-[12px]">{problem.replicaName}</p>}
					{problemHref !== undefined && (
						<Button variant="outline" size="sm" nativeButton={false} render={<Link to={problemHref} />}>
							<LogsIcon />
							View logs
						</Button>
					)}
				</Banner>
			)}
			<div className="grid min-w-0 gap-4">
				{groupReplicas(deployment.replicas, deployment.currentRevision).map((group) => (
					<section key={group.label} aria-label={group.label}>
						<h3 className="mt-0 mb-2 text-[12px] font-medium text-fg3">
							{group.label} <span className="font-mono">({group.replicas.length})</span>
						</h3>
						<ul className="m-0 list-none divide-y divide-line rounded-lg border border-line p-0">
							{group.replicas.length === 0 && <li className="px-3 py-2.5 text-[12px] text-fg3">No replicas</li>}
							{group.replicas.map((replica) => {
								const state = replicaState(replica.state);
								const countdown = stoppingCountdown(replica, now);
								return (
									<li
										key={replica.uid}
										className={cn(
											"flex min-w-0 flex-col gap-1.5 px-3 py-2.5",
											replica.state === "Stopped" && "opacity-50",
										)}
									>
										<div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
											<span className="min-w-0 break-all font-mono text-[12px]">{replica.name}</span>
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
						</ul>
					</section>
				))}
			</div>
			<section aria-label="Deployment activity">
				<h3 className="mt-0 mb-2 text-[12px] font-medium text-fg3">Activity</h3>
				<div
					ref={followActivity}
					// oxlint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The scroll region needs keyboard access.
					tabIndex={0}
					role="region"
					aria-label="Activity feed"
					className="max-h-64 overflow-y-auto overscroll-contain rounded-lg border border-line bg-background p-3 [scrollbar-gutter:stable] focus-visible:outline-2 focus-visible:outline-ring"
				>
					{rows.length === 0 && <p className="m-0 text-[12px] text-fg3">Waiting for activity…</p>}
					<ol className="m-0 flex list-none flex-col gap-2 p-0">
						{rows.map((row) => (
							<li
								key={row.key}
								className="grid grid-cols-[3.5rem_0.5rem_minmax(0,1fr)] items-baseline gap-2 text-[12px]"
							>
								<span className="font-mono text-fg3 tabular-nums">{row.time}</span>
								<span
									className={cn(
										"size-1.5 self-start mt-1.5 rounded-full bg-info",
										row.severity === "ok" && "bg-ok-glyph",
										row.severity === "warn" && "bg-warn",
										row.severity === "error" && "bg-bad-fg",
									)}
								>
									<span className="sr-only">{row.severity}</span>
								</span>
								<span className="whitespace-pre-wrap [overflow-wrap:anywhere]">
									{row.replicaName !== "" && <span className="mr-2 font-mono text-fg3">{row.replicaName}</span>}
									{row.message}
								</span>
							</li>
						))}
					</ol>
				</div>
			</section>
		</div>
	);
}
