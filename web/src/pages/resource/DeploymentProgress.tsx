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
import { ChevronDownIcon, LogsIcon, ScrollTextIcon, Undo2Icon, XIcon } from "lucide-react";
import { useId, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { Checkbox } from "@/components/design/Checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/design/Collapsible";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { CopyId } from "@/components/design/CopyId";
import { MetaLine } from "@/components/design/MetaLine";
import { deploymentPhaseStyle } from "@/components/design/StatusBadge";
import { StatusGlyph, type Glyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { useNow } from "@/hooks/useNow";
import { cancelable, canceledNote, placementReadyLabel, rolloutReadyLabel } from "@/lib/activity-deploys";
import {
	deploymentActor,
	deploymentMethod,
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
import { cn, formatShortId } from "@/lib/utils";

import { isBuildFinished } from "./builds";
import {
	clockLabel,
	deploymentElapsedMs,
	problemLogsHref,
	progressDeployments,
	readyLabel,
	rolloutActivity,
	rolloutSteps,
	servingDeployments,
	showsCard,
	type RolloutStep,
	type StepState,
} from "./deployment-progress";
import { ActivityFeed, ProblemBanner, ReplicaGroups } from "./DeploymentRollout";
import { formatFullTime } from "./format";
import { depImage, depTag, type RegionView } from "./model";

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

function ProgressPanel({
	members,
	logsHref,
	canWrite,
	onRollback,
}: {
	members: readonly [RolloutMember, ...RolloutMember[]];
	logsHref: string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const titleId = useId();
	const [selectedRegion, setSelectedRegion] = useState(members[0].region.name);
	const member = members.find((m) => m.region.name === selectedRegion) ?? members[0];
	const { region, deployment, previous, activity, connectionError } = member;
	const multiRegion = members.length > 1;
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
	const restorable = finished.filter((r) => r.previous !== undefined);
	const confirmCancel = () => {
		cancel.mutate(
			{ deploymentId: deployment.id, rollbackFinishedRegions: rollbackFinished && restorable.length > 0 },
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
	const desired = placement?.live === true ? deployment : region.current;
	const desiredLive = placementIn(desired, region.name)?.live === true;
	const serving = servingDeployments(placementIn(desired, region.name)?.replicas ?? []);
	const failed = phase === DeploymentStatus.FAILED || phase === DeploymentStatus.CRASHED;
	const canceled = phase === DeploymentStatus.CANCELED;
	const title = deploymentPhaseStyle(phase).label;
	const message = deployment.commit?.message ?? "";
	const headline = message === "" ? depTag(deployment) : message;
	const glyph: Glyph = failed
		? "err"
		: canceled || phase === DeploymentStatus.SUPERSEDED
			? "off"
			: live
				? "spin"
				: problem !== undefined || phase === DeploymentStatus.DEGRADED
					? "warn"
					: "ok";
	const feed = rolloutActivity(activity, region.name, placement?.currentRevision ?? 0n);
	const elapsed = deploymentElapsedMs(deployment, nowMs, feed.restart);
	const commit = deployment.commit?.sha ?? build?.gitCommit;
	const { path, tag, digest } = parseImage(depImage(deployment));
	const fields: [string, string][] = [];
	const image = depImage(deployment);
	if (image !== "") fields.push(["Image", tag === "" ? path : `${path}:${tag}`]);
	if (digest !== "" || build?.imageDigest) fields.push(["Digest", digest !== "" ? digest : (build?.imageDigest ?? "")]);
	if (size?.cpu !== undefined && size.memory !== undefined)
		fields.push(["Size", `${formatCpu(parseCpuMilli(size.cpu))} · ${formatMemory(parseMemMi(size.memory))}`]);
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
	const stepNote = (step: RolloutStep): string => {
		if (step.key === "build") {
			if (step.state === "done") return build?.imageDigest ? build.imageDigest.slice(0, 15) : "Image ready";
			if (step.state === "active") return "Building the image";
			return step.state === "failed" ? (problem?.message ?? "Build failed") : "";
		}
		if (step.state === "pending") return "";
		if (step.state === "failed") return problem?.message ?? "Rollout failed";
		if (step.state === "stopped") return "Canceled";
		if (multiRegion) return rolloutReadyLabel(deployment);
		return readyLabel(deployment, placement);
	};
	return (
		<section aria-labelledby={titleId} className="flex min-w-0 flex-col gap-4 rounded-card bg-panel px-5 py-[18px]">
			<div className="flex flex-wrap items-start gap-3">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
						<span className="m-0 min-w-0 text-[15.5px] font-medium break-words">{headline}</span>
						<h2
							id={titleId}
							className={cn("m-0 inline-flex items-center gap-1.5 text-[12.5px] font-medium", failed && "text-err")}
						>
							<StatusGlyph glyph={glyph} />
							{title}
						</h2>
						{elapsed !== undefined && (
							<span
								title={
									feed.restart === undefined
										? "Time since the deployment was created"
										: "Time since the restart started"
								}
								className="font-mono text-[12px] text-fg3 tabular-nums"
							>
								{clockLabel(elapsed)}
							</span>
						)}
					</div>
					{canceled && canceledNote(deployment) !== undefined && (
						<p className="m-0 text-[12.5px] text-fg2">{canceledNote(deployment)}</p>
					)}
					<MetaLine className="text-[12.5px] text-fg3">
						<span className="inline-flex items-center gap-2">
							<span className="font-mono text-[12px] text-fg2" title={commit ?? deployment.id}>
								{commit ? shortSha(commit) : formatShortId(deployment.id)}
							</span>
							<CopyId
								value={deployment.id}
								title="Copy deployment ID"
								className="-ml-1 size-5 justify-center rounded-[4px] p-0 text-fg3 hover:bg-panel2 [&_svg]:size-3"
							>
								<span className="sr-only">Copy deployment ID</span>
							</CopyId>
						</span>
						{(deployment.actorName !== "" || deployment.actorId !== "") && (
							<span>
								{deploymentActor(deployment)}
								{deploymentMethod(deployment) !== "" && ` via ${deploymentMethod(deployment)}`}
							</span>
						)}
						<span>
							{multiRegion ? (
								`${live ? "Rolling out to" : "Rolled out to"} ${members.length.toString()} regions`
							) : (
								<span className="font-mono text-[12px]">{region.name}</span>
							)}
						</span>
						{!commit && image !== "" && (
							<span className="min-w-0 break-all font-mono text-[12px] text-fg2">{image}</span>
						)}
					</MetaLine>
				</div>
				<div className="flex shrink-0 flex-wrap items-center gap-1.5">
					{failed ? (
						<>
							{logsHref !== undefined && problemLogsHref(logsHref, deployment) === undefined && (
								<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
									<LogsIcon />
									View logs
								</Button>
							)}
							{previous !== undefined && (
								<Button
									variant="outline"
									size="sm"
									disabled={!canWrite}
									onClick={() => {
										onRollback(previous, deployment);
									}}
								>
									<Undo2Icon />
									Roll back to {depTag(previous)}
								</Button>
							)}
						</>
					) : (
						logsHref !== undefined && (
							<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
								<ScrollTextIcon />
								Logs
							</Button>
						)
					)}
					{live ? (
						cancelable(deployment) && (
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
						)
					) : (
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
					)}
				</div>
			</div>
			{multiRegion && (
				<ToggleGroup
					aria-label="Rollout regions"
					value={[region.name]}
					onValueChange={(values: string[]) => {
						if (values[0] !== undefined) setSelectedRegion(values[0]);
					}}
					className="flex-wrap"
				>
					{members.map((m) => {
						const memberPlacement = placementIn(m.deployment, m.region.name);
						return (
							<ToggleGroupItem key={m.region.name} value={m.region.name} className="font-mono">
								<StatusGlyph glyph={deploymentPhaseStyle(regionPhase(m)).glyph} />
								{m.region.name}
								{memberPlacement !== undefined && (
									<span className="text-fg3 tabular-nums">{placementReadyLabel(m.deployment, memberPlacement)}</span>
								)}
							</ToggleGroupItem>
						);
					})}
				</ToggleGroup>
			)}
			{connectionError !== undefined && (
				<p role="status" className="m-0 text-[13px] text-warn-fg">
					Disconnected. Reconnecting… {connectionError}
				</p>
			)}
			<ProblemBanner deployment={deployment} logsHref={logsHref} />
			<div className="grid min-w-0 gap-5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
				<div className="flex min-w-0 flex-col gap-4">
					<StepRail steps={steps} note={stepNote} />
					<ReplicaGroups deployment={deployment} region={region.name} />
				</div>
				<ActivityFeed
					startMs={feed.restart === undefined ? maybeTsMs(deployment.createdAt) : feed.restart.startMs}
					activity={feed.entries}
				/>
			</div>
			{desired !== undefined && desiredLive && (
				<dl aria-label="Deployment versions" className="m-0 flex flex-wrap gap-x-6 gap-y-2 text-[12px]">
					<div className="flex flex-wrap items-center gap-x-2">
						<dt className="text-fg3">Desired deployment</dt>
						<dd className="m-0">
							<CopyId value={desired.id} className="font-mono text-[12px]">
								{formatShortId(desired.id)}
							</CopyId>
						</dd>
					</div>
					<div className="flex flex-wrap items-center gap-x-2">
						<dt className="text-fg3">Serving deployments</dt>
						<dd className="m-0 flex flex-wrap items-center gap-x-2 gap-y-1">
							{serving.length === 0 && <span className="text-fg3">No ready replicas reported</span>}
							{serving.map(({ id, readyReplicas }) => (
								<span key={id} className="inline-flex items-center gap-1">
									<CopyId value={id} className="font-mono text-[12px]">
										{formatShortId(id)}
									</CopyId>
									<span className="text-fg3">({readyReplicas} ready)</span>
								</span>
							))}
						</dd>
					</div>
				</dl>
			)}
			{fields.length > 0 && (
				<Collapsible>
					<CollapsibleTrigger className="group flex items-center gap-1 text-[12.5px] text-fg3 hover:text-foreground">
						What’s being deployed
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
				description="Regions that don't run it yet go back to the version that served them before. Regions that already run it keep it."
				confirmLabel="Cancel deployment"
				pendingLabel="Canceling…"
				cancelLabel="Keep deploying"
				pending={cancel.isPending}
				onConfirm={confirmCancel}
				onClose={() => {
					setConfirmingCancel(false);
					setRollbackFinished(false);
				}}
			>
				{finished.length > 0 && (
					<FinishedRegions
						finished={finished}
						restorable={restorable}
						rollback={rollbackFinished}
						onRollback={setRollbackFinished}
					/>
				)}
			</ConfirmDialog>
		</section>
	);
}

function FinishedRegions({
	finished,
	restorable,
	rollback,
	onRollback,
}: {
	finished: readonly FinishedRegion[];
	restorable: readonly FinishedRegion[];
	rollback: boolean;
	onRollback: (next: boolean) => void;
}) {
	const timePref = useTimePreferences();
	const checkboxId = useId();
	const targets = new Set(restorable.map((r) => r.previous?.id));
	const target = targets.size === 1 ? depTag(restorable[0]?.previous) : "their previous versions";
	return (
		<div className="flex flex-col gap-2.5 text-left text-[12.5px]">
			<ul aria-label="Regions that already run it" className="m-0 flex list-none flex-col gap-1 p-0">
				{finished.map(({ region, previous }) => {
					const deployedMs =
						previous === undefined ? undefined : (maybeTsMs(previous.startedAt) ?? tsMs(previous.createdAt));
					return (
						<li key={region} className="flex min-w-0 flex-wrap gap-x-2">
							<span className="font-mono text-foreground">{region}</span>
							{previous === undefined || deployedMs === undefined ? (
								<span className="text-fg3">First deployment here, nothing to roll back to</span>
							) : (
								<span className="text-fg2">
									previous {depTag(previous)}
									{previous.commit !== undefined && ` · ${shortSha(previous.commit.sha)}`}
									{` · deployed ${formatFullTime(deployedMs, false, timePref)}`}
								</span>
							)}
						</li>
					);
				})}
			</ul>
			{restorable.length > 0 && (
				<label htmlFor={checkboxId} className="flex items-center gap-2 text-foreground">
					<Checkbox
						id={checkboxId}
						checked={rollback}
						onCheckedChange={(next) => {
							onRollback(next);
						}}
					/>
					Also roll back {restorable.map((r) => r.region).join(", ")} to {target}
				</label>
			)}
		</div>
	);
}

const STEP_GLYPH: Record<StepState, Glyph> = {
	pending: "draft",
	active: "spin",
	done: "ok",
	failed: "err",
	stopped: "off",
};

function StepRail({ steps, note }: { steps: RolloutStep[]; note: (step: RolloutStep) => string }) {
	return (
		<ol aria-label="Rollout steps" className="m-0 flex list-none flex-col p-0">
			{steps.map((step, i) => (
				<li key={step.key} className="flex gap-2.5">
					<span className="flex flex-col items-center pt-0.5">
						<StatusGlyph glyph={STEP_GLYPH[step.state]} />
						{i < steps.length - 1 && <span aria-hidden className="mt-1 w-px flex-1 bg-line2" />}
					</span>
					<span className={cn("flex min-w-0 flex-col gap-0.5", i < steps.length - 1 && "pb-3.5")}>
						<span className={cn("text-[13px] font-medium", step.state === "pending" && "text-fg3")}>
							{step.label}
							<span className="sr-only">, {step.state}</span>
						</span>
						{note(step) !== "" && <span className="font-mono text-[11.5px] break-words text-fg3">{note(step)}</span>}
					</span>
				</li>
			))}
		</ol>
	);
}

export function DeploymentProgress({
	regions,
	logsHref,
	canWrite,
	onRollback,
}: {
	regions: RegionView[];
	logsHref: string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
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
	return [...Map.groupBy(members, (m) => m.deployment.id).values()].map(([first, ...rest]) => {
		if (first === undefined) return null;
		return (
			<ProgressPanel
				key={first.deployment.id}
				members={[first, ...rest]}
				logsHref={logsHref}
				canWrite={canWrite}
				onRollback={onRollback}
			/>
		);
	});
}
