import "./Activity.css";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { useQuery } from "@tanstack/react-query";
import { XIcon, ScrollTextIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import {
	RECENT_MS,
	activityDeployments,
	clockLabel,
	deployPhase,
	deploySteps,
	finishedLabel,
	rolloutGroups,
	reportedReadyLabel,
	withoutRunningDeploys,
	type StepState,
} from "@/lib/activity-deploys";
import { type Alert, alertedResourceCount, buildAlerts } from "@/lib/alerts";
import { getServiceSpec, isInFlight } from "@/lib/deployment-utils";
import { buildLogsPath, observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { eventReasonLabel } from "@/pages/observability/events";
import { problemLogsHref } from "@/pages/resource/deployment-progress";

const CLOCK_TICK_MS = 60_000;
const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;
const STEP_NAMES = { build: "Build image", deploy: "Deploy replicas" } as const;
const STEP_CLASS: Record<StepState, string> = {
	pending: "bg-line2",
	active: "dwa-active bg-info",
	retry: "dwa-active bg-info",
	done: "bg-fg3",
	skipped: "bg-line",
};
const PULSE = "M2 12h4l3-8 6 16 3-8h4";

function ActivityCard({ children }: { children: ReactNode }) {
	return (
		<li className="dwa flex flex-col gap-2 rounded-[10px] bg-panel px-4 py-3.5 shadow-[inset_0_0_0_1px_var(--line)]">
			{children}
		</li>
	);
}

function CardName({
	name,
	href,
	note,
	onNavigate,
}: {
	name: string;
	href: string;
	note: string;
	onNavigate: () => void;
}) {
	return (
		<div className="flex min-w-0 items-baseline gap-2">
			<Link
				to={href}
				onClick={onNavigate}
				className="shrink-0 text-[15.5px] font-medium underline-offset-[3px] hover:underline"
			>
				{name}
			</Link>
			{note !== "" && <span className="min-w-0 truncate text-[13px] text-fg3">{note}</span>}
		</div>
	);
}

function RegionChips({ regions }: { regions: readonly string[] }) {
	return (
		<div className="flex flex-wrap gap-1">
			{regions.map((region) => (
				<span
					key={region}
					className="dwa-chip rounded-[4px] bg-background px-[7px] py-0.5 font-mono text-[11.5px] text-fg2 shadow-[inset_0_0_0_1px_var(--line)]"
				>
					{region}
				</span>
			))}
		</div>
	);
}

function CardActions({
	name,
	resourceHref,
	logsHref,
	logsLabel = "Logs",
	onNavigate,
}: {
	name: string;
	resourceHref: string;
	logsHref: string;
	logsLabel?: string;
	onNavigate: () => void;
}) {
	return (
		<div className="mt-0.5 flex gap-1.5">
			{[
				{ to: resourceHref, label: `Open ${name}` },
				{ to: logsHref, label: logsLabel, icon: <ScrollTextIcon className="text-fg3" /> },
			].map((action) => (
				<Button
					key={action.label}
					variant="outline"
					nativeButton={false}
					render={<Link to={action.to} onClick={onNavigate} />}
					className="h-7 bg-background px-2.5 text-[12.5px]"
				>
					{action.icon}
					{action.label}
				</Button>
			))}
		</div>
	);
}

function AlertCard({
	alert,
	resourceHref,
	logsHref,
	nowMs,
	onNavigate,
}: {
	alert: Alert;
	resourceHref: string;
	logsHref: string;
	nowMs: number;
	onNavigate: () => void;
}) {
	return (
		<ActivityCard>
			<div className="flex items-center justify-between gap-2.5 [&>span:first-child]:text-[13px]">
				<ResourceStatusBadge status={alert.status} />
				{alert.reportedMs !== undefined && (
					<span className="font-mono text-[12px] text-fg3">since {agoLabel(alert.reportedMs, nowMs)}</span>
				)}
			</div>
			<CardName name={alert.name} href={resourceHref} note={alert.description} onNavigate={onNavigate} />
			<RegionChips regions={[alert.region]} />
			{alert.message !== "" && <p className="m-0 font-mono text-[12px] break-words text-fg2">{alert.message}</p>}
			<CardActions name={alert.name} resourceHref={resourceHref} logsHref={logsHref} onNavigate={onNavigate} />
		</ActivityCard>
	);
}

function DeployCard({
	rollout,
	ready,
	name,
	resourceHref,
	logsHref,
	buildLogsHref,
	onNavigate,
}: {
	rollout: readonly [Deployment, ...Deployment[]];
	ready: ReadonlyMap<string, number>;
	name: string;
	resourceHref: string;
	logsHref: string;
	buildLogsHref: string | undefined;
	onNavigate: () => void;
}) {
	const deployment = rollout.find((d) => d.problem !== undefined) ?? rollout[0];
	const running = isInFlight(deployment.status);
	const nowMs = useNow(running ? 1000 : CLOCK_TICK_MS).getTime();
	const buildId = getServiceSpec(deployment)?.build?.buildId ?? "";
	const build = useConnectQuery(getBuild, { buildId }, { enabled: buildId !== "" }).data?.build;
	const phase = deployPhase(deployment);
	const problem = deployment.problem;
	const steps = deploySteps(deployment);
	const created = maybeTsMs(deployment.createdAt) ?? nowMs;
	const buildLogs =
		buildLogsHref !== undefined &&
		(deployment.status === DeploymentStatus.BUILDING || problem?.reason === "build_failed");
	return (
		<ActivityCard>
			<div className="flex items-center justify-between gap-2.5">
				<span className="inline-flex items-center gap-2 text-[12.5px] font-medium whitespace-nowrap">
					<StatusGlyph glyph={phase.glyph} />
					{phase.label}
				</span>
				<span
					className={cn("font-mono text-[12px]", running ? "text-foreground" : "text-fg3")}
					title={running ? "Time since the deployment was created" : "How long it ran"}
				>
					{running ? clockLabel(nowMs - created) : finishedLabel(deployment, nowMs)}
				</span>
			</div>
			<CardName name={name} href={resourceHref} note={build?.gitCommitMessage ?? ""} onNavigate={onNavigate} />
			<RegionChips regions={rollout.map((d) => d.region)} />
			{running ? (
				<div className="flex items-center gap-2.5 text-[13px]">
					<span aria-hidden className="flex shrink-0 gap-[3px]">
						{(["build", "deploy"] as const).map((step) => (
							<i key={step} className={cn("dwa-step h-1 w-3.5 rounded-[2px]", STEP_CLASS[steps[step]])} />
						))}
					</span>
					<span className="flex min-w-0 flex-col gap-px">
						<b className="font-medium">{STEP_NAMES[steps.current]}</b>
						{problem !== undefined && problem.message !== "" && (
							<span className="truncate font-mono text-[12px] text-fg3">{problem.message}</span>
						)}
					</span>
				</div>
			) : problem !== undefined ? (
				<div className="flex flex-col gap-px text-[13px]">
					<b className={cn("font-medium", deployment.status === DeploymentStatus.FAILED ? "text-err" : "text-warn")}>
						{eventReasonLabel(problem.reason)}
					</b>
					{problem.message !== "" && <span className="text-fg2">{problem.message}</span>}
				</div>
			) : (
				deployment.status !== DeploymentStatus.FAILED &&
				deployment.status !== DeploymentStatus.CANCELED && (
					<span className="text-[12.5px] text-fg2">{reportedReadyLabel(rollout, ready)}</span>
				)
			)}
			<CardActions
				name={name}
				resourceHref={resourceHref}
				logsHref={buildLogs ? buildLogsHref : (problemLogsHref(logsHref, deployment) ?? logsHref)}
				logsLabel={buildLogs ? "Build logs" : "Logs"}
				onNavigate={onNavigate}
			/>
		</ActivityCard>
	);
}

export function AlertsDrawer() {
	const [open, setOpen] = useState(false);
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const resources = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: activeOrgId !== null && activeWorkspaceId !== null && active !== undefined,
	});
	const sinceMs = Math.floor(nowMs / CLOCK_TICK_MS) * CLOCK_TICK_MS - RECENT_MS;
	const deploymentsQuery = useConnectQuery(
		listDeployments,
		{
			workspaceId: activeWorkspaceId ?? "",
			environmentId: active?.id ?? "",
			since: timestampFromMs(sinceMs),
			pageSize: 50,
		},
		{
			enabled: activeWorkspaceId !== null && active !== undefined,
			refetchInterval: (q) =>
				(q.state.data?.deployments ?? []).some((d) => isInFlight(d.status)) ? POLL_MS : IDLE_POLL_MS,
		},
	);

	if (activeOrgId === null || activeWorkspaceId === null || active === undefined) return null;

	const names = new Map((resources.data?.resources ?? []).map((r) => [r.id, r.name]));
	const deployments = activityDeployments(deploymentsQuery.data?.deployments ?? [], nowMs).filter((d) =>
		names.has(d.resourceId),
	);
	const ready = new Map(
		(resources.data?.resources ?? []).flatMap((r) =>
			r.environmentStatuses.flatMap((e) => e.regions.map((g) => [g.deploymentId, g.readyReplicas] as const)),
		),
	);
	const alerts = withoutRunningDeploys(buildAlerts(resources.data?.resources ?? [], active.id), deployments);
	const count = alertedResourceCount(alerts);
	const envParams = { env: active.name };
	const close = () => {
		setOpen(false);
	};
	const deployCard = ([d, ...rest]: Deployment[]) => {
		if (d === undefined) return null;
		const name = names.get(d.resourceId) ?? "";
		const buildId = getServiceSpec(d)?.build?.buildId ?? "";
		return (
			<DeployCard
				key={d.id}
				rollout={[d, ...rest]}
				ready={ready}
				name={name}
				resourceHref={resourcePath(activeOrgId, activeWorkspaceId, d.resourceId)}
				logsHref={observabilityPath(activeOrgId, activeWorkspaceId, "logs", { ...envParams, r: [name] })}
				buildLogsHref={buildId === "" ? undefined : buildLogsPath(activeOrgId, activeWorkspaceId, buildId)}
				onNavigate={close}
			/>
		);
	};

	return (
		<>
			<Button
				variant="outline"
				aria-label={`Activity, ${count.toString()} active`}
				aria-haspopup="dialog"
				aria-expanded={open}
				onClick={() => {
					setOpen(true);
				}}
				className="act-btn gap-[7px] border-line bg-background pr-2.5 pl-[9px] text-fg2 hover:border-line2 hover:bg-background hover:text-foreground aria-expanded:border-line2 aria-expanded:bg-background aria-expanded:text-foreground dark:border-line dark:bg-background dark:hover:border-line2 dark:hover:bg-background dark:aria-expanded:bg-background"
			>
				<span className="act-ico" aria-hidden>
					<svg
						viewBox="0 0 24 24"
						width="15"
						height="15"
						fill="none"
						stroke="currentColor"
						strokeWidth="2"
						strokeLinecap="round"
						strokeLinejoin="round"
					>
						<path className="act-base" d={PULSE} />
						<path className="act-pulse" pathLength="100" d={PULSE} />
					</svg>
				</span>
				<span className="hidden md:inline">Activity</span>
				{count > 0 && <span className="act-n font-mono">{count}</span>}
			</Button>
			<Sheet open={open} onOpenChange={setOpen}>
				<SheetContent showCloseButton={false}>
					<SheetHeader>
						<SheetTitle className="font-medium">Activity</SheetTitle>
						{count > 0 && <span className="act-n act-heading-count font-mono">{count}</span>}
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label="Close activity"
							onClick={close}
							className="absolute top-[18px] right-4"
						>
							<XIcon />
						</Button>
					</SheetHeader>
					<SheetBody className="p-4">
						{alerts.length === 0 && deployments.length === 0 ? (
							<p className="px-2 py-12 text-center text-[13.5px] text-fg3">Nothing needs attention.</p>
						) : (
							<ul className="flex flex-col gap-2.5">
								{rolloutGroups(deployments.filter((d) => isInFlight(d.status))).map(deployCard)}
								{alerts.map((a) => (
									<AlertCard
										key={a.key}
										alert={a}
										resourceHref={resourcePath(activeOrgId, activeWorkspaceId, a.resourceId)}
										logsHref={observabilityPath(activeOrgId, activeWorkspaceId, "logs", { ...envParams, r: [a.name] })}
										nowMs={nowMs}
										onNavigate={close}
									/>
								))}
								{rolloutGroups(deployments.filter((d) => !isInFlight(d.status))).map(deployCard)}
							</ul>
						)}
					</SheetBody>
					<SheetFooter>
						<Button
							variant="link"
							nativeButton={false}
							render={
								<Link to={observabilityPath(activeOrgId, activeWorkspaceId, "events", envParams)} onClick={close} />
							}
						>
							All events
						</Button>
					</SheetFooter>
				</SheetContent>
			</Sheet>
		</>
	);
}
