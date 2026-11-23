import { useMutation, useQuery } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import {
	cancelDeployment,
	getDeploymentCancelImpact,
} from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import {
	DeploymentStatus,
	type Activity,
	type Deployment,
	type FinishedRegion,
} from "@gen/loco/deployment/v1/deployment_pb";
import { ChevronDownIcon, GitCompareIcon, XIcon } from "lucide-react";
import { useId, useState, type ReactElement } from "react";
import { toast } from "@/lib/toast";

import { Button } from "@/components/design/Button";
import { Checkbox } from "@/components/design/Checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/design/Collapsible";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { StatusGlyph, type Glyph } from "@/components/design/StatusGlyph";
import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { useNow } from "@/hooks/useNow";
import { cancelable, canceledNote, placementReadyLabel, tookLabel } from "@/lib/activity-deploys";
import {
	deploymentProblem,
	getServiceSpec,
	isInFlight,
	placementIn,
	placementPhase,
	regionTarget,
	shortSha,
} from "@/lib/deployment-utils";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { parseImage } from "@/lib/image";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { maybeTsMs, tsMs } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { cn } from "@/lib/utils";

import { buildTookMs, isBuildFinished } from "./builds";
import {
	deploymentElapsedMs,
	laneState,
	progressDeployments,
	rolloutActivity,
	rolloutHeadline,
	rolloutSteps,
	servingDeployments,
	showsCard,
	triggeredByLabel,
	type RolloutStep,
} from "./deployment-progress";
import { LogsLink, ProblemBanner, ReplicaGroups } from "./DeploymentRollout";
import { formatFullTime } from "./format";
import { depImage, depTag, regionDeployment, tookMs, type RegionView } from "./model";
import { RolloutFlow, type FlowNode } from "./RolloutFlow";

function regionPhase(m: RolloutMember): DeploymentStatus {
	const placement = placementIn(m.deployment, m.region.name);
	return placement === undefined ? m.deployment.status : placementPhase(placement.status);
}

interface RolloutMember {
	region: RegionView;
	deployment: Deployment;
	previous: Deployment | undefined;
	activity: readonly Activity[];
	connectionError: string | undefined;
}

const HEADLINE_GLYPH: Record<"run" | "ok" | "warn" | "err" | "off", Glyph> = {
	run: "spin",
	ok: "ok",
	warn: "warn",
	err: "err",
	off: "off",
};

function ProgressPanel({
	members,
	workspaceId,
	logsHref,
	regionLogsHref,
	buildLogsHref,
	canWrite,
	onCompare,
	deployTimeoutSeconds,
}: {
	members: readonly [RolloutMember, ...RolloutMember[]];
	workspaceId: string;
	deployTimeoutSeconds: number | undefined;
	logsHref: string | undefined;
	/** The logs of one region's replicas. */
	regionLogsHref: (region: string) => string | undefined;
	buildLogsHref: (buildId: string) => string | undefined;
	canWrite: boolean;
	/** Opens the spec of `to` compared with the spec of `from`. */
	onCompare: (from: Deployment, to: Deployment) => void;
}) {
	const titleId = useId();
	const [selectedRegion, setSelectedRegion] = useState(members[0].region.name);
	const [openStep, setOpenStep] = useState<string | null>(null);
	const member = members.find((m) => m.region.name === selectedRegion) ?? members[0];
	const { region, deployment, previous, activity, connectionError } = member;
	const phase = deployment.status;
	const live = isInFlight(phase);
	const nowMs = useNow(live ? 1000 : 60000).getTime();
	const spec = getServiceSpec(deployment);
	const size = regionTarget(spec, region.name);
	const placement = placementIn(deployment, region.name);
	const problem = deploymentProblem(deployment);
	const buildId = spec?.build?.buildId ?? "";
	const buildQuery = useQuery(
		getBuild,
		{ buildId },
		{
			enabled: buildId !== "",
			refetchInterval: (query) =>
				query.state.data?.build !== undefined && !isBuildFinished(query.state.data.build.status) ? 15000 : false,
		},
	);
	const [dismissed, setDismissed] = useState(false);
	const [confirmingCancel, setConfirmingCancel] = useState(false);
	const [rollbackFinished, setRollbackFinished] = useState(false);
	const cancel = useMutation(cancelDeployment);
	const impactQuery = useQuery(
		getDeploymentCancelImpact,
		{ deploymentId: deployment.id },
		{ enabled: confirmingCancel },
	);
	if (dismissed) return null;
	const finished = impactQuery.data?.finishedRegions ?? [];
	const confirmCancel = () => {
		cancel.mutate(
			{ deploymentId: deployment.id, rollbackFinishedRegions: rollbackFinished },
			{
				onSuccess: (res) => {
					toast.success(
						res.rollbackDeploymentIds.length > 0
							? `Canceled ${depTag(deployment)} and queued a rollback`
							: `Canceled ${depTag(deployment)}`,
					);
					setConfirmingCancel(false);
					setRollbackFinished(false);
				},
				onError: (err) => {
					toastConnectError(err, `Could not cancel ${depTag(deployment)}`);
				},
			},
		);
	};
	const build = buildQuery.data?.build;
	const canceled = phase === DeploymentStatus.CANCELED;
	const failed = phase === DeploymentStatus.FAILED || phase === DeploymentStatus.CRASHED;
	const stillServing =
		failed || (live && problem !== undefined)
			? servingDeployments(region.status?.replicas ?? []).find((d) => d.id !== deployment.id)
			: undefined;
	const servingDeployment = regionDeployment(region, stillServing?.id);
	const servingNow =
		servingDeployment === undefined || stillServing === undefined
			? undefined
			: { deployment: servingDeployment, readyReplicas: stillServing.readyReplicas };
	const feed = rolloutActivity(activity, region.name, live ? (placement?.currentRevision ?? 0n) : 0n);
	const elapsed = deploymentElapsedMs(deployment, nowMs, feed.restart);
	const headline = rolloutHeadline(deployment, elapsed);
	const buildHref = buildId === "" ? undefined : buildLogsHref(buildId);
	const { path, tag, digest } = parseImage(depImage(deployment));
	const fields: [string, string][] = [];
	const image = depImage(deployment);
	if (image !== "") fields.push(["Image", tag === "" ? path : `${path}:${tag}`]);
	if (digest !== "" || build?.imageDigest) fields.push(["Digest", digest !== "" ? digest : (build?.imageDigest ?? "")]);
	if (size?.cpu !== undefined && size.memory !== undefined)
		fields.push(["Size", `${formatCpu(parseCpuMilli(size.cpu))}, ${formatMemory(parseMemMi(size.memory))}`]);
	else {
		if (size?.cpu !== undefined) fields.push(["CPU", formatCpu(parseCpuMilli(size.cpu))]);
		if (size?.memory !== undefined) fields.push(["Memory", formatMemory(parseMemMi(size.memory))]);
	}
	if (size?.minReplicas !== undefined && size.maxReplicas !== undefined)
		fields.push([
			"Replicas",
			size.minReplicas === size.maxReplicas
				? String(size.minReplicas)
				: `${size.minReplicas}–${size.maxReplicas} (autoscaling)`,
		]);
	if (spec?.port !== undefined) fields.push(["Port", String(spec.port)]);
	if (spec?.healthCheck !== undefined)
		fields.push(["Health check", `${spec.healthCheck.path} every ${spec.healthCheck.intervalSeconds}s`]);
	const steps = rolloutSteps(deployment, buildId !== "");
	const buildStep = steps.find((step) => step.key === "build");
	const deployState = steps.find((step) => step.key === "deploy")?.state ?? "pending";
	const buildTook = build === undefined ? undefined : buildTookMs(build);
	const buildNode: FlowNode = {
		key: "build",
		label: "Build",
		state: buildStep?.state ?? "skipped",
		duration:
			buildStep === undefined
				? "skipped"
				: buildStep.state === "pending" || buildTook === undefined
					? ""
					: tookLabel(buildTook),
		extra: "",
	};
	const deployTook = tookMs(deployment);
	const lanes = members.map((m) => {
		const state = laneState(regionPhase(m), deployState, problem !== undefined);
		const memberPlacement = placementIn(m.deployment, m.region.name);
		const started = state !== "pending" && memberPlacement !== undefined;
		return {
			region: m.region.name,
			node: {
				key: `rg:${m.region.name}`,
				label: "Deploy",
				state,
				duration: state === "done" && deployTook !== undefined ? tookLabel(deployTook) : "",
				extra: started ? `${placementReadyLabel(m.deployment, memberPlacement)} ready` : "",
			},
		};
	});
	const toggleStep = (key: string) => {
		setOpenStep(openStep === key ? null : key);
		if (key.startsWith("rg:")) setSelectedRegion(key.slice(3));
	};
	const stepNote = (step: RolloutStep | undefined): string => {
		if (step === undefined || step.state === "pending") return "";
		if (step.key === "build") {
			if (step.state === "done") return build?.imageDigest ? build.imageDigest.slice(0, 15) : "Image ready";
			if (step.state === "active") return "Building the image";
			return step.state === "failed" ? (problem?.message ?? "Build failed") : "";
		}
		if (step.state === "failed") return problem?.message ?? "Rollout failed";
		if (step.state === "stopped") return "Canceled";
		return step.state === "done" ? "Ready" : "";
	};
	const openRegion = openStep?.startsWith("rg:") === true ? openStep.slice(3) : undefined;
	const detailNote = openStep === "build" ? stepNote(buildStep) : stepNote(steps.find((step) => step.key === "deploy"));
	const detailHref = openStep === "build" ? buildHref : regionLogsHref(openRegion ?? "");
	return (
		<section aria-labelledby={titleId} className="flex min-w-0 flex-col gap-4 rounded-card bg-panel px-5 py-[18px]">
			<div className="flex flex-wrap items-start gap-3">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
						<StatusGlyph glyph={HEADLINE_GLYPH[headline.tone]} className="size-4" />
						<h2
							id={titleId}
							className={cn("m-0 text-[15.5px] font-medium break-words", headline.tone === "err" && "text-err")}
						>
							{headline.title}
						</h2>
						{headline.when !== "" && (
							<span
								title={
									feed.restart === undefined
										? "Time since the deployment was created"
										: "Time since the restart started"
								}
								className="font-mono text-[13px] text-fg3 tabular-nums"
							>
								{headline.when}
							</span>
						)}
					</div>
					{canceled && canceledNote(deployment) !== undefined && (
						<p className="m-0 text-[12.5px] text-fg2">{canceledNote(deployment)}</p>
					)}
				</div>
				<div className="flex shrink-0 flex-wrap items-center gap-1.5">
					{previous !== undefined && previous.id !== deployment.id && (
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								onCompare(previous, deployment);
							}}
						>
							<GitCompareIcon />
							Compare spec
						</Button>
					)}
					{cancelable(deployment) ? (
						<Button
							variant="outline"
							size="sm"
							disabled={!canWrite}
							onClick={() => {
								setConfirmingCancel(true);
							}}
						>
							Cancel
						</Button>
					) : (
						!live && (
							<Button
								variant="ghost"
								size="icon-sm"
								aria-label="Dismiss"
								onClick={() => {
									setDismissed(true);
								}}
							>
								<XIcon />
							</Button>
						)
					)}
				</div>
			</div>
			{connectionError !== undefined && (
				<p role="status" className="m-0 text-[13px] text-warn-fg">
					Disconnected. Reconnecting… {connectionError}
				</p>
			)}
			<ProblemBanner
				deployment={deployment}
				workspaceId={workspaceId}
				logsHref={logsHref}
				serving={servingNow}
				deployTimeoutSeconds={deployTimeoutSeconds}
			/>
			<div className="flex min-w-0 flex-col gap-3">
				<RolloutFlow
					triggeredBy={triggeredByLabel(deployment)}
					deploymentId={deployment.id}
					build={buildNode}
					lanes={lanes}
					open={openStep}
					onToggle={toggleStep}
				/>
				{openStep !== null && (
					<div className="relative flex flex-col gap-2.5 rounded-lg bg-background px-3.5 py-3 ring-1 ring-line animate-in fade-in-0 slide-in-from-top-1 motion-reduce:animate-none">
						{detailNote !== "" && <p className="m-0 pr-56 font-mono text-[12px] text-fg2">{detailNote}</p>}
						{openRegion !== undefined && <ReplicaGroups deployment={deployment} region={openRegion} />}
						{detailHref !== undefined && <LogsLink href={detailHref} />}
					</div>
				)}
			</div>
			{fields.length > 0 && (
				<Collapsible>
					<CollapsibleTrigger className="group flex items-center gap-1 text-[12.5px] text-fg3 hover:text-foreground">
						Spec
						<ChevronDownIcon className="size-3 transition-transform group-data-panel-open:rotate-180 motion-reduce:transition-none" />
					</CollapsibleTrigger>
					<CollapsibleContent>
						<dl className="mt-2.5 mb-0 grid gap-x-6 gap-y-2.5 sm:grid-cols-2">
							{fields.map(([label, value]) => (
								<div key={label}>
									<dt className="text-[11.5px] text-fg3">{label}</dt>
									<dd className="mt-0.5 ml-0 break-all font-mono text-[12.5px]">{value}</dd>
								</div>
							))}
						</dl>
					</CollapsibleContent>
				</Collapsible>
			)}
			{buildQuery.error !== null && (
				<div className="flex items-center gap-3 text-[12.5px] text-fg3">
					<span>{getErrorMessage(buildQuery.error, "Failed to load build details")}</span>
					<Button
						variant="outline"
						onClick={() => {
							void buildQuery.refetch();
						}}
					>
						Retry
					</Button>
				</div>
			)}
			<ConfirmDialog
				open={confirmingCancel}
				title={`Cancel ${depTag(deployment)}?`}
				description="Regions where its replicas haven't all become available yet go back to the version that served them before, or stop running it if none did. Regions where they have keep it."
				confirmLabel="Cancel deployment"
				pendingLabel="Canceling…"
				cancelLabel="Keep deployment"
				pending={cancel.isPending}
				onConfirm={confirmCancel}
				onClose={() => {
					setConfirmingCancel(false);
					setRollbackFinished(false);
				}}
			>
				{finished.length > 0 && (
					<FinishedRegions finished={finished} rollback={rollbackFinished} onRollback={setRollbackFinished} />
				)}
			</ConfirmDialog>
		</section>
	);
}

function FinishedRegions({
	finished,
	rollback,
	onRollback,
}: {
	finished: readonly FinishedRegion[];
	rollback: boolean;
	onRollback: (next: boolean) => void;
}) {
	const timePref = useTimePreferences();
	const checkboxId = useId();
	const restorable = finished.filter((r) => r.previous !== undefined);
	const targets = new Set(restorable.map((r) => r.previous?.id));
	const target = targets.size === 1 ? depTag(restorable[0]?.previous) : "their previous versions";
	return (
		<div className="flex flex-col gap-2.5 text-left text-[12.5px]">
			<ul aria-label="Regions that already run it" className="m-0 flex list-none flex-col gap-1 p-0">
				{finished.map(({ region, previous }) => (
					<li key={region} className="flex min-w-0 flex-wrap gap-x-2">
						<span className="font-mono text-foreground">{region}</span>
						{previous === undefined ? (
							<span className="text-fg3">First deployment here, nothing to roll back to</span>
						) : (
							<span className="text-fg2">
								previous {depTag(previous)}
								{previous.commit !== undefined && `, ${shortSha(previous.commit.sha)}`}
								{`, deployed ${formatFullTime(maybeTsMs(previous.startedAt) ?? tsMs(previous.createdAt), false, timePref)}`}
							</span>
						)}
					</li>
				))}
			</ul>
			{restorable.length > 0 && (
				<label htmlFor={checkboxId} className="flex items-center gap-2 text-foreground">
					<Checkbox id={checkboxId} checked={rollback} onCheckedChange={onRollback} />
					Also roll back {restorable.map((r) => r.region).join(", ")} to {target}
				</label>
			)}
		</div>
	);
}

export function DeploymentProgress({
	regions,
	workspaceId,
	fallback,
	logsHref,
	regionLogsHref,
	buildLogsHref,
	canWrite,
	onCompare,
	deployTimeoutSeconds,
}: {
	regions: RegionView[];
	workspaceId: string;
	/** Shown while no deployment has a card. */
	fallback?: ReactElement | undefined;
	/** The resource's rollout deadline. */
	deployTimeoutSeconds: number | undefined;
	logsHref: string | undefined;
	/** The logs of one region's replicas. */
	regionLogsHref: (region: string) => string | undefined;
	buildLogsHref: (buildId: string) => string | undefined;
	canWrite: boolean;
	/** Opens the spec of `to` compared with the spec of `from`. */
	onCompare: (from: Deployment, to: Deployment) => void;
}) {
	const progress = progressDeployments(regions);
	const watchOf = useDeploymentWatch([...new Set(progress.map(({ deployment }) => deployment.id))]);
	const nowMs = useNow(15_000).getTime();
	const members = progress.flatMap(({ region, deployment, previous }): RolloutMember[] => {
		const watched = watchOf(deployment.id);
		const current =
			watched.deployment !== undefined && tsMs(watched.deployment.updatedAt) >= tsMs(deployment.updatedAt)
				? watched.deployment
				: deployment;
		if (!showsCard(current, nowMs)) return [];
		return [{ region, deployment: current, previous, activity: watched.activity, connectionError: watched.error }];
	});
	const panels = [...Map.groupBy(members, (m) => m.deployment.id).values()].map(([first, ...rest]) => {
		if (first === undefined) return null;
		return (
			<ProgressPanel
				key={first.deployment.id}
				members={[first, ...rest]}
				workspaceId={workspaceId}
				logsHref={logsHref}
				regionLogsHref={regionLogsHref}
				buildLogsHref={buildLogsHref}
				canWrite={canWrite}
				onCompare={onCompare}
				deployTimeoutSeconds={deployTimeoutSeconds}
			/>
		);
	});
	return <>{panels.length > 0 ? panels : fallback}</>;
}
