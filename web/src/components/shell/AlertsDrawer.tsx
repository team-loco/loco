import "./Activity.css";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { RotateCwIcon, XIcon, ScrollTextIcon } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useEffect, useEffectEvent, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "@/lib/toast";

import { Button } from "@/components/design/Button";
import { Kbd } from "@/components/design/Kbd";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ROLLOUT_STYLE, StatusBadge, resourceStatusStyle, type StatusStyle } from "@/components/design/StatusBadge";
import { NavArrow } from "@/components/design/NavArrow";
import { PlacementChips, RegionChip } from "@/components/design/PlacementChips";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { useAuth } from "@/auth/AuthProvider";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceEvents } from "@/hooks/useWorkspaceEvents";
import { ACTIVITY_EVENT_TYPES } from "@/lib/runtime-events";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import {
	ACTIVITY_PAGE,
	RECENT_MS,
	activityDeployments,
	activityEvents,
	clockLabel,
	deployPhase,
	deploySteps,
	finishedLabel,
	reportedReadyLabel,
	rollingOutRegions,
	runningDeployRegions,
	withoutRunningDeploys,
	type ActivityLevel,
	type StepState,
} from "@/lib/activity-deploys";
import { type Alert, alertedResourceCount, buildAlerts } from "@/lib/alerts";
import { deploymentHeadline, deploymentProblem, getServiceSpec, isInFlight } from "@/lib/deployment-utils";
import { ACTIVITY_PARAM, buildLogsPath, observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, DAY_MS, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { asSeverity, eventItem, severityStyle } from "@/pages/observability/events";
import { PROBLEM_REASONS, problemLogsHref } from "@/pages/resource/deployment-progress";

import { degradedToast, deployToasts, type DeployToast } from "./deploy-toasts";
import { useDeployEvents } from "./useDeployEvents";

const CLOCK_TICK_MS = 60_000;
const LEVEL_PARSER = parseAsStringLiteral(["all", "warn"] as const satisfies readonly ActivityLevel[]).withDefault(
	"all",
);
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
			<Link to={href} onClick={onNavigate} className="group/nav flex shrink-0 text-[15.5px] font-medium">
				{name}
				<NavArrow />
			</Link>
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
			<div className="flex items-center justify-between gap-2.5">
				<span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
					<StatusLabel {...resourceStatusStyle(alert.status)} />
					{alert.rollout !== undefined && <StatusLabel {...ROLLOUT_STYLE[alert.rollout]} />}
				</span>
				{alert.sinceMs !== undefined && (
					<span className="font-mono text-[12px] text-fg3">started {agoLabel(alert.sinceMs, nowMs)}</span>
				)}
			</div>
			<CardName name={alert.name} href={resourceHref} note={alert.description} onNavigate={onNavigate} />
			<div className="flex">
				<RegionChip>{alert.region}</RegionChip>
			</div>
			{alert.message !== "" && (
				<p className="m-0 rounded-[6px] bg-background px-3 py-2.5 font-mono text-[12px] leading-[1.55] break-words text-foreground">
					{alert.message}
				</p>
			)}
			<CardActions name={alert.name} resourceHref={resourceHref} logsHref={logsHref} onNavigate={onNavigate} />
		</ActivityCard>
	);
}

const SEVERITY_LABEL = { error: "Error", warning: "Warning", normal: "Info" } as const;

function EventCards({
	workspaceId,
	resources,
	resourceId,
	level,
	sinceMs,
	resourceHref,
	logsHref,
	nowMs,
	onNavigate,
}: {
	workspaceId: string;
	resources: ReadonlyMap<string, Resource>;
	resourceId: string | undefined;
	level: ActivityLevel;
	sinceMs: number;
	resourceHref: (resourceId: string) => string;
	logsHref: (slug: string) => string;
	nowMs: number;
	onNavigate: () => void;
}) {
	const names = new Map([...resources].map(([id, r]) => [id, r.name]));
	const { events } = useWorkspaceEvents(workspaceId, names, {
		types: ACTIVITY_EVENT_TYPES,
		...(resourceId === undefined ? {} : { resourceIds: [resourceId] }),
		startMs: sinceMs,
	});
	return activityEvents(events.map(eventItem), level).map((e) => {
		const severity = asSeverity(e.severity);
		return (
			<ActivityCard key={e.id}>
				<div className="flex items-center justify-between gap-2.5">
					<span className="inline-flex items-center gap-2 text-[13px]">
						<StatusGlyph glyph={e.type === "resource.recovered" ? "ok" : severityStyle(severity).glyph} />
						{SEVERITY_LABEL[severity]}
					</span>
					<span className="font-mono text-[12px] text-fg3">{agoLabel(e.ts, nowMs)}</span>
				</div>
				<CardName name={e.resourceName} href={resourceHref(e.resourceId)} note={e.title} onNavigate={onNavigate} />
				{e.detail !== "" && <span className="font-mono text-[12px] break-words text-fg2">{e.detail}</span>}
				<div className="mt-0.5 flex">
					<Button
						variant="outline"
						nativeButton={false}
						render={<Link to={logsHref(resources.get(e.resourceId)?.slug ?? "")} onClick={onNavigate} />}
						className="h-7 bg-background px-2.5 text-[12.5px]"
					>
						<ScrollTextIcon className="text-fg3" />
						Logs
					</Button>
				</div>
			</ActivityCard>
		);
	});
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
	const reason = problem === undefined ? undefined : PROBLEM_REASONS[problem.reason];
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
			<CardName name={name} href={resourceHref} note={deploymentHeadline(deployment)} onNavigate={onNavigate} />
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
						{reason ?? problem.message}
					</b>
					{reason !== undefined ? (
						<span className="text-fg2">{problem.message}</span>
					) : (
						problem.detail !== "" && <span className="text-fg2">{problem.detail}</span>
					)}
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
	// The drawer is open while the page's address carries the activity parameter, whose value names the resource it shows.
	const [activity, setActivity] = useQueryState(ACTIVITY_PARAM, parseAsString);
	const open = activity !== null;
	const [level, setLevel] = useQueryState("activity-level", LEVEL_PARSER);
	const [openedAtMs, setOpenedAtMs] = useState(0);
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const [shown, setShown] = useState(ACTIVITY_PAGE);
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	// When the drawer started watching this environment: the event stream starts here, deployments that last
	// changed before it toast only when they change again, and the closed drawer lists from 30 minutes before it.
	const [watchedSinceMs] = useState(Date.now);
	const resources = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: activeOrgId !== null && activeWorkspaceId !== null,
		// The drawer remounts for each environment; the workspace's resource list stays the one the shell loaded.
		refetchOnMount: false,
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
	const heard = useDeployEvents(activeOrgId, activeWorkspaceId, active?.id, watchedSinceMs, (events) => {
		const names = new Map((resourceList ?? []).map((r) => [r.id, r.name]));
		show(events.flatMap((e) => degradedToast(e, names) ?? []));
	});
	const openedTickMs = Math.floor((openedAtMs || watchedSinceMs) / CLOCK_TICK_MS) * CLOCK_TICK_MS;
	const sinceMs = openedTickMs - RECENT_MS;
	// The day before the drawer opened, rounded to the clock tick so the query key is stable.
	const eventsSinceMs = openedTickMs - DAY_MS;
	const deploymentsQuery = useConnectQuery(
		listDeployments,
		{
			workspaceId: activeWorkspaceId ?? "",
			environmentId: active?.id ?? "",
			startTime: timestampFromMs(sinceMs),
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
		const { seen, toasts } = deployToasts(seenRef.current, fresh, names, viewerId, watchedSinceMs);
		seenRef.current = seen;
		showDeploymentToasts(toasts);
	}, [fresh, resourceList, watchedSinceMs, viewerId]);

	if (activeOrgId === null || activeWorkspaceId === null || active === undefined) return null;

	const byId = new Map((resources.data?.resources ?? []).map((r) => [r.id, r]));
	// Placeholder rows from an earlier opening can belong to another environment.
	const deployments = activityDeployments(deploymentsQuery.data?.deployments ?? [], nowMs).filter(
		(d) => byId.has(d.resourceId) && d.environmentId === active.id,
	);
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
	const allAlerts = withoutRunningDeploys(buildAlerts(resources.data?.resources ?? [], active.id), running);
	const count = alertedResourceCount(allAlerts);
	const shownResource = (resources.data?.resources ?? []).find((r) => r.slug === activity);
	const matches = (resourceId: string) => shownResource === undefined || shownResource.id === resourceId;
	const alerts = allAlerts.filter((a) => matches(a.resourceId));
	const levelMatches = (d: Deployment) => level === "all" || ["warn", "err"].includes(deployPhase(d).glyph);
	const shownDeployments = deployments.filter((d) => matches(d.resourceId) && levelMatches(d));
	const finishedShown = shownDeployments.filter((d) => !isInFlight(d.status));
	const envParams = { env: active.slug };
	const openChange = (next: boolean) => {
		if (next) setOpenedAtMs(Date.now());
		void setActivity(next ? "" : null);
		if (!next) void setLevel(null);
	};
	const pickResource = (slug: string) => {
		void setActivity(slug, { history: "replace" });
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
						<SheetTitle className="text-[26px] leading-[1.15] tracking-[-0.03em]">
							{shownResource === undefined ? "Workspace activity" : `${shownResource.name} activity`}
						</SheetTitle>
						<Tooltip>
							<TooltipTrigger
								render={
									<Button
										variant="ghost"
										size="icon-sm"
										aria-label="Close workspace activity"
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
					<div className="flex shrink-0 items-center gap-2 px-4 pt-3">
						<ToggleGroup
							variant="segmented"
							aria-label="Level"
							value={[level]}
							onValueChange={(v: string[]) => {
								void setLevel(v[0] === "warn" ? "warn" : "all", { history: "replace" });
							}}
						>
							<ToggleGroupItem value="all">All</ToggleGroupItem>
							<ToggleGroupItem value="warn">Warnings</ToggleGroupItem>
						</ToggleGroup>
						<FilterMenu
							label="Resource"
							value={shownResource?.slug ?? ""}
							allValue=""
							options={[
								{ value: "", label: "All resources" },
								...(resources.data?.resources ?? []).map((r) => ({ value: r.slug, label: r.name })),
							]}
							onChange={pickResource}
						/>
					</div>
					<SheetBody className="p-4">
						<ul className="flex flex-col gap-2.5">
							{shownDeployments.filter((d) => isInFlight(d.status)).map(deployCard)}
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
							<EventCards
								workspaceId={activeWorkspaceId}
								resources={byId}
								resourceId={shownResource?.id}
								level={level}
								sinceMs={eventsSinceMs}
								resourceHref={(id) => resourcePath(activeOrgId, activeWorkspaceId, id)}
								logsHref={(slug) =>
									observabilityPath(activeOrgId, activeWorkspaceId, "logs", { ...envParams, r: [slug] })
								}
								nowMs={nowMs}
								onNavigate={close}
							/>
							{finishedShown.slice(0, shown).map(deployCard)}
							{finishedShown.length > shown && (
								<li className="flex justify-center">
									<Button
										variant="ghost"
										onClick={() => {
											setShown(shown + ACTIVITY_PAGE);
										}}
									>
										Show {Math.min(ACTIVITY_PAGE, finishedShown.length - shown)} more
									</Button>
								</li>
							)}
							<li className="hidden px-2 py-12 text-center text-[13.5px] text-fg3 only:block">
								{level === "warn" ? "No warnings." : "Nothing here yet."}
							</li>
						</ul>
					</SheetBody>
				</SheetContent>
			</Sheet>
		</>
	);
}
