import "./Activity.css";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { RotateCwIcon, XIcon, ScrollTextIcon } from "lucide-react";
import { useEffect, useEffectEvent, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { Kbd } from "@/components/design/Kbd";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ROLLOUT_STYLE, StatusBadge, resourceStatusStyle, type StatusStyle } from "@/components/design/StatusBadge";
import { PlacementChips } from "@/components/design/PlacementChips";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { useAuth } from "@/auth/AuthProvider";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceEvents } from "@/hooks/useWorkspaceEvents";
import { RUNTIME_EVENT_TYPES } from "@/lib/runtime-events";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import {
	ACTIVITY_PAGE,
	RECENT_MS,
	activityDeployments,
	recentHealthEvents,
	clockLabel,
	deployPhase,
	deploySteps,
	finishedLabel,
	reportedReadyLabel,
	rollingOutRegions,
	runningDeployRegions,
	withoutRunningDeploys,
	type StepState,
} from "@/lib/activity-deploys";
import { type Alert, alertedResourceCount, buildAlerts } from "@/lib/alerts";
import { deploymentProblem, getServiceSpec, isInFlight } from "@/lib/deployment-utils";
import { buildLogsPath, observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, DAY_MS, maybeTsMs, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { problemLogsHref } from "@/pages/resource/deployment-progress";

import { degradedToast, deployToasts, type DeployToast } from "./deploy-toasts";
import { useDeployEvents } from "./useDeployEvents";

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
const TOAST_MS = 9000;
const LIVE_TOAST_MS = 5000;

function showDeployToast(t: DeployToast, onView: () => void) {
	const show = { deploying: toast, warning: toast.warning, failed: toast.error, live: toast.success }[t.tone];
	// Each change replaces the deployment's toast, so every option is set, even when empty.
	show(t.title, {
		id: `deploy-${t.deploymentId}`,
		description: t.description,
		action: { label: "View", onClick: onView },
		icon: t.tone === "deploying" ? <RotateCwIcon className="size-4" /> : undefined,
		duration: t.tone === "failed" ? Infinity : t.tone === "live" ? LIVE_TOAST_MS : TOAST_MS,
	});
}

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
	region = "",
	note,
	onNavigate,
}: {
	name: string;
	href: string;
	region?: string;
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
			{region !== "" && <span className="shrink-0 font-mono text-[12px] text-fg3">{region}</span>}
			{note !== "" && <span className="min-w-0 truncate text-[13px] text-fg3">{note}</span>}
		</div>
	);
}

function StatusLabel({ glyph, label }: StatusStyle) {
	return (
		<span className="inline-flex items-center gap-2">
			<StatusGlyph glyph={glyph} />
			{label}
		</span>
	);
}

function CardActions({
	name,
	resourceHref,
	logsHref,
	logsLabel = "Logs",
	primary = false,
	onNavigate,
}: {
	name: string;
	resourceHref: string;
	logsHref: string;
	logsLabel?: string;
	primary?: boolean;
	onNavigate: () => void;
}) {
	return (
		<div className="mt-0.5 flex gap-1.5">
			{[
				{ to: resourceHref, label: `Open ${name}`, solid: primary },
				{ to: logsHref, label: logsLabel, icon: <ScrollTextIcon className="text-fg3" />, solid: false },
			].map((action) => (
				<Button
					key={action.label}
					variant={action.solid ? "default" : "outline"}
					nativeButton={false}
					render={<Link to={action.to} onClick={onNavigate} />}
					className={cn("h-7 px-2.5 text-[12.5px]", !action.solid && "bg-background")}
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
			<div className="flex items-center justify-between gap-2.5">
				<span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
					<StatusLabel {...resourceStatusStyle(alert.status)} />
					{alert.rollout !== undefined && <StatusLabel {...ROLLOUT_STYLE[alert.rollout]} />}
				</span>
				{alert.sinceMs !== undefined && (
					<span className="font-mono text-[12px] text-fg3">started {agoLabel(alert.sinceMs, nowMs)}</span>
				)}
			</div>
			<CardName
				name={alert.name}
				href={resourceHref}
				region={alert.region}
				note={alert.description}
				onNavigate={onNavigate}
			/>
			{alert.message !== "" && (
				<p className="m-0 rounded-[6px] bg-background px-3 py-2.5 font-mono text-[12px] leading-[1.55] break-words text-foreground">
					{alert.message}
				</p>
			)}
			<CardActions name={alert.name} resourceHref={resourceHref} logsHref={logsHref} primary onNavigate={onNavigate} />
		</ActivityCard>
	);
}

function HealthEventCards({
	workspaceId,
	names,
	sinceMs,
	resourceHref,
	nowMs,
	onNavigate,
}: {
	workspaceId: string;
	names: ReadonlyMap<string, string>;
	sinceMs: number;
	resourceHref: (resourceId: string) => string;
	nowMs: number;
	onNavigate: () => void;
}) {
	const { events } = useWorkspaceEvents(workspaceId, names, { types: RUNTIME_EVENT_TYPES, startMs: sinceMs });
	return recentHealthEvents(events, nowMs).map((e) => (
		<ActivityCard key={e.id}>
			<div className="flex items-center justify-between gap-2.5">
				<span className="inline-flex items-center gap-2 text-[12.5px] font-medium whitespace-nowrap">
					<StatusGlyph glyph={e.type === "resource.degraded" ? "warn" : "ok"} />
					{e.title}
				</span>
				<span className="font-mono text-[12px] text-fg3">{agoLabel(tsMs(e.timestamp), nowMs)}</span>
			</div>
			<CardName name={e.resourceName} href={resourceHref(e.resourceId)} note={e.reason} onNavigate={onNavigate} />
		</ActivityCard>
	));
}

function DeployCard({
	deployment,
	ready,
	name,
	resourceHref,
	logsHref,
	buildLogsHref,
	onNavigate,
}: {
	deployment: Deployment;
	ready: ReadonlyMap<string, number>;
	name: string;
	resourceHref: string;
	logsHref: string;
	buildLogsHref: string | undefined;
	onNavigate: () => void;
}) {
	const running = isInFlight(deployment.status);
	const nowMs = useNow(running ? 1000 : CLOCK_TICK_MS).getTime();
	const phase = deployPhase(deployment);
	const problem = deploymentProblem(deployment);
	const steps = deploySteps(deployment);
	const created = maybeTsMs(deployment.createdAt) ?? nowMs;
	const buildLogs =
		buildLogsHref !== undefined &&
		(deployment.status === DeploymentStatus.BUILDING || problem?.reason === "build_failed");
	return (
		<ActivityCard>
			<div className="flex items-center justify-between gap-2.5">
				<StatusBadge {...phase} />
				<span
					className={cn("font-mono text-[12px]", running ? "text-foreground" : "text-fg3")}
					title={running ? "Time since the deployment was created" : "How long it ran"}
				>
					{running ? clockLabel(nowMs - created) : finishedLabel(deployment, nowMs)}
				</span>
			</div>
			<CardName name={name} href={resourceHref} note={deployment.commit?.message ?? ""} onNavigate={onNavigate} />
			<PlacementChips placements={deployment.placements} />
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
					<b
						className={cn(
							"font-medium",
							deployment.status === DeploymentStatus.FAILED || deployment.status === DeploymentStatus.CRASHED
								? "text-err"
								: "text-warn",
						)}
					>
						{problem.message}
					</b>
					{problem.detail !== "" && <span className="text-fg2">{problem.detail}</span>}
				</div>
			) : (
				deployment.status !== DeploymentStatus.FAILED &&
				deployment.status !== DeploymentStatus.CRASHED &&
				deployment.status !== DeploymentStatus.CANCELED && (
					<span className="text-[12.5px] text-fg2">{reportedReadyLabel(deployment, ready)}</span>
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
	const [openedAtMs, setOpenedAtMs] = useState(0);
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const [shown, setShown] = useState(ACTIVITY_PAGE);
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	// Closed, the deployment list covers the 30 minutes before the shell showed this environment, onwards.
	const [watched, setWatched] = useState(() => ({ environmentId: active?.id, sinceMs: Date.now() }));
	if (watched.environmentId !== active?.id) setWatched({ environmentId: active?.id, sinceMs: nowMs });
	const resources = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: activeOrgId !== null && activeWorkspaceId !== null,
	});
	const navigate = useNavigate();
	const viewerId = useAuth().user?.id ?? "";
	const resourceList = resources.data?.resources;
	// While the drawer is open it shows these changes, so no toasts show.
	const show = (toasts: readonly DeployToast[]) => {
		if (open || activeOrgId === null || activeWorkspaceId === null) return;
		for (const t of toasts)
			showDeployToast(t, () => {
				void navigate(resourcePath(activeOrgId, activeWorkspaceId, t.resourceId));
			});
	};
	const showDeploymentToasts = useEffectEvent(show);
	const { heard, startedMsRef } = useDeployEvents(activeOrgId, activeWorkspaceId, active?.id, (events) => {
		const names = new Map((resourceList ?? []).map((r) => [r.id, r.name]));
		show(events.flatMap((e) => degradedToast(e, names) ?? []));
	});
	const sinceMs = Math.floor((openedAtMs || watched.sinceMs) / CLOCK_TICK_MS) * CLOCK_TICK_MS - RECENT_MS;
	const deploymentsQuery = useConnectQuery(
		listDeployments,
		{
			workspaceId: activeWorkspaceId ?? "",
			environmentId: active?.id ?? "",
			since: timestampFromMs(sinceMs),
			pageSize: 50,
		},
		{
			// Closed, the drawer reads deployments only after a deployment event, and polls only while one is in flight.
			enabled: (open || heard) && activeWorkspaceId !== null && active !== undefined,
			placeholderData: keepPreviousData,
			meta: { persist: false },
			refetchInterval: (q) =>
				(q.state.data?.deployments ?? []).some((d) => isInFlight(d.status)) ? POLL_MS : open ? IDLE_POLL_MS : false,
		},
	);

	const seenRef = useRef(new Map<string, string>());
	const fresh = deploymentsQuery.isPlaceholderData ? undefined : deploymentsQuery.data?.deployments;
	// Sonner is outside React, so each new page of deployments syncs into it here.
	useEffect(() => {
		if (fresh === undefined || resourceList === undefined) return;
		const names = new Map(resourceList.map((r) => [r.id, r.name]));
		const { seen, toasts } = deployToasts(seenRef.current, fresh, names, viewerId, startedMsRef.current);
		seenRef.current = seen;
		showDeploymentToasts(toasts);
	}, [fresh, resourceList, startedMsRef, viewerId]);

	if (activeOrgId === null || activeWorkspaceId === null || active === undefined) return null;

	const byId = new Map((resources.data?.resources ?? []).map((r) => [r.id, r]));
	// Placeholder rows from an earlier opening can belong to another environment.
	const deployments = activityDeployments(deploymentsQuery.data?.deployments ?? [], nowMs).filter(
		(d) => byId.has(d.resourceId) && d.environmentId === active.id,
	);
	const finished = deployments.filter((d) => !isInFlight(d.status));
	const ready = new Map<string, number>();
	for (const region of (resources.data?.resources ?? []).flatMap((r) =>
		r.environmentStatuses.flatMap((e) => e.regions),
	)) {
		ready.set(region.servingDeploymentId, (ready.get(region.servingDeploymentId) ?? 0) + region.readyReplicas);
	}
	const running =
		open && deploymentsQuery.data !== undefined
			? runningDeployRegions(deployments)
			: rollingOutRegions(resources.data?.resources ?? [], active.id);
	const alerts = withoutRunningDeploys(buildAlerts(resources.data?.resources ?? [], active.id), running);
	const count = alertedResourceCount(alerts);
	const envParams = { env: active.slug };
	const openChange = (next: boolean) => {
		if (next) setOpenedAtMs(Date.now());
		setOpen(next);
	};
	const close = () => {
		openChange(false);
	};
	const deployCard = (d: Deployment) => {
		const resource = byId.get(d.resourceId);
		const buildId = getServiceSpec(d)?.build?.buildId ?? "";
		return (
			<DeployCard
				key={d.id}
				deployment={d}
				ready={ready}
				name={resource?.name ?? ""}
				resourceHref={resourcePath(activeOrgId, activeWorkspaceId, d.resourceId)}
				logsHref={observabilityPath(activeOrgId, activeWorkspaceId, "logs", {
					...envParams,
					r: [resource?.slug ?? ""],
				})}
				buildLogsHref={buildId === "" ? undefined : buildLogsPath(activeOrgId, activeWorkspaceId, buildId)}
				onNavigate={close}
			/>
		);
	};

	return (
		<>
			<Button
				variant="outline"
				aria-label={`Workspace activity, ${count.toString()} active`}
				aria-haspopup="dialog"
				aria-expanded={open}
				onClick={() => {
					openChange(true);
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
				<span className="hidden md:inline">Workspace activity</span>
				{count > 0 && <span className="act-n font-mono">{count}</span>}
			</Button>
			<Sheet open={open} onOpenChange={openChange}>
				<SheetContent showCloseButton={false}>
					<SheetHeader>
						<SheetTitle className="text-[26px] leading-[1.15] tracking-[-0.03em]">Workspace activity</SheetTitle>
						{count > 0 && <span className="act-n act-heading-count font-mono">{count}</span>}
						<Tooltip>
							<TooltipTrigger
								render={
									<Button
										variant="ghost"
										size="icon-sm"
										aria-label="Close activity"
										onClick={close}
										className="absolute top-[18px] right-4"
									/>
								}
							>
								<XIcon />
							</TooltipTrigger>
							<TooltipContent className="flex items-center gap-2">
								Close
								<Kbd>esc</Kbd>
							</TooltipContent>
						</Tooltip>
					</SheetHeader>
					<SheetBody className="p-4">
						<ul className="flex flex-col gap-2.5">
							{deployments.filter((d) => isInFlight(d.status)).map(deployCard)}
							{alerts.map((a) => (
								<AlertCard
									key={a.key}
									alert={a}
									resourceHref={resourcePath(activeOrgId, activeWorkspaceId, a.resourceId)}
									logsHref={observabilityPath(activeOrgId, activeWorkspaceId, "logs", { ...envParams, r: [a.slug] })}
									nowMs={nowMs}
									onNavigate={close}
								/>
							))}
							<HealthEventCards
								workspaceId={activeWorkspaceId}
								names={new Map([...byId].map(([id, r]) => [id, r.slug]))}
								sinceMs={sinceMs}
								resourceHref={(id) => resourcePath(activeOrgId, activeWorkspaceId, id)}
								nowMs={nowMs}
								onNavigate={close}
							/>
							{finished.slice(0, shown).map(deployCard)}
							{finished.length > shown && (
								<li className="flex justify-center">
									<Button
										variant="ghost"
										onClick={() => {
											setShown(shown + ACTIVITY_PAGE);
										}}
									>
										Show {Math.min(ACTIVITY_PAGE, finished.length - shown)} more
									</Button>
								</li>
							)}
							<li className="hidden px-2 py-12 text-center text-[13.5px] text-fg3 only:block">
								Nothing needs attention.
							</li>
						</ul>
					</SheetBody>
					<SheetFooter>
						<Button
							variant="link"
							nativeButton={false}
							render={
								<Link
									to={observabilityPath(activeOrgId, activeWorkspaceId, "events", { ...envParams, range: DAY_MS })}
									onClick={close}
								/>
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
