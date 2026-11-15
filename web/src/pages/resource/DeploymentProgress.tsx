import { useQuery } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { DeploymentPhase, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ChevronDownIcon, LogsIcon, Undo2Icon } from "lucide-react";
import { useId } from "react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/design/Collapsible";
import { CopyId } from "@/components/design/CopyId";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { useNow } from "@/hooks/useNow";
import { deploymentActor, deploymentMethod, getServiceSpec, isInFlight, shortSha } from "@/lib/deployment-utils";
import { getErrorMessage } from "@/lib/error-handler";
import { parseImage } from "@/lib/image";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { maybeTsMs, shortAgoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { formatBuildDuration, isBuildFinished } from "./builds";
import { deploymentSteps, progressDeployments } from "./deployment-progress";
import { depImage, depTag, type RegionView } from "./model";

function ProgressPanel({
	region,
	deployment,
	previous,
	phase,
	message,
	updatedMs,
	environmentName,
	logsHref,
	canWrite,
	onRollback,
}: {
	region: RegionView;
	deployment: Deployment;
	previous: Deployment | undefined;
	phase: DeploymentPhase;
	message: string;
	updatedMs: number | undefined;
	environmentName: string;
	logsHref: string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const titleId = useId();
	const live = isInFlight(phase);
	const nowMs = useNow(live ? 1000 : 60000).getTime();
	const spec = getServiceSpec(deployment);
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
	const build = buildQuery.data?.build;
	const steps = deploymentSteps({ ...deployment, status: phase }, build);
	const failed = phase === DeploymentPhase.FAILED;
	const canceled = phase === DeploymentPhase.CANCELED;
	const title = failed
		? `Deployment of ${depTag(deployment)} failed`
		: canceled
			? `Deployment of ${depTag(deployment)} canceled`
			: live
				? `Deploying ${depTag(deployment)}`
				: `Deployed ${depTag(deployment)}`;
	const created = maybeTsMs(deployment.createdAt);
	const completed = maybeTsMs(deployment.completedAt);
	const status = region.status?.deploymentId === deployment.id ? region.status : undefined;
	const reported = maybeTsMs(status?.reportedAt);
	const deploymentUpdated = updatedMs ?? maybeTsMs(deployment.updatedAt);
	const useRegionalMessage =
		status?.message && reported !== undefined && (deploymentUpdated === undefined || reported > deploymentUpdated);
	const controllerMessage = useRegionalMessage ? status.message : message !== "" ? message : (status?.message ?? "");
	const updated = useRegionalMessage ? reported : deploymentUpdated;
	const commit = deployment.commit?.sha ?? build?.gitCommit;
	const { path, tag, digest } = parseImage(depImage(deployment));
	const fields: [string, string][] = [];
	const image = depImage(deployment);
	if (image !== "") fields.push(["Image", tag === "" ? path : `${path}:${tag}`]);
	if (digest !== "" || build?.imageDigest) fields.push(["Digest", digest !== "" ? digest : (build?.imageDigest ?? "")]);
	if (spec?.cpu !== undefined && spec.memory !== undefined)
		fields.push(["Size", `${formatCpu(parseCpuMilli(spec.cpu))} · ${formatMemory(parseMemMi(spec.memory))}`]);
	else {
		if (spec?.cpu !== undefined) fields.push(["CPU", formatCpu(parseCpuMilli(spec.cpu))]);
		if (spec?.memory !== undefined) fields.push(["Memory", formatMemory(parseMemMi(spec.memory))]);
	}
	if (spec?.minReplicas !== undefined && spec.maxReplicas !== undefined)
		fields.push([
			"Replicas",
			spec.minReplicas === spec.maxReplicas
				? String(spec.minReplicas)
				: `${spec.minReplicas}–${spec.maxReplicas} (autoscaling)`,
		]);
	if (spec?.port !== undefined) fields.push(["Port", String(spec.port)]);
	if (spec?.healthCheck !== undefined)
		fields.push(["Health check", `${spec.healthCheck.path} every ${spec.healthCheck.intervalSeconds}s`]);
	return (
		<section aria-labelledby={titleId} className="flex min-w-0 flex-col gap-3.5 rounded-card bg-panel px-5 py-[18px]">
			<div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 sm:grid-cols-[auto_1fr_auto_auto]">
				<StatusGlyph glyph={failed ? "err" : canceled ? "off" : live ? "spin" : "ok"} className="size-4 shrink-0" />
				<div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2.5 gap-y-1">
					<h2 id={titleId} className={cn("m-0 break-words font-mono text-[15px] font-medium", failed && "text-err")}>
						{title}
					</h2>
					<span className="text-[13px] text-fg3">
						to {environmentName === "" ? region.name : `${environmentName} · ${region.name}`}
					</span>
				</div>
				{created !== undefined && (live || completed !== undefined) && (
					<span className="col-start-2 row-start-2 flex flex-col text-[11.5px] text-fg3 sm:col-start-3 sm:row-start-1 sm:items-end">
						<span>
							{live
								? "Elapsed"
								: completed === undefined
									? "Duration"
									: `${failed ? "Failed" : canceled ? "Canceled" : "Completed"} ${shortAgoLabel(completed, nowMs)}`}
						</span>
						<span className="font-mono text-[14px] text-foreground">
							{!live && "ran for "}
							{formatBuildDuration(Math.max(0, (completed ?? nowMs) - created))}
						</span>
					</span>
				)}
				<CopyId value={deployment.id} title="Copy deployment ID" className="col-start-3 row-start-1 sm:col-start-4">
					<span className="sr-only">Copy deployment ID</span>
				</CopyId>
			</div>
			<div className="-mt-2 flex flex-wrap items-baseline gap-x-2.5 gap-y-1 pl-7 text-[12.5px] text-fg3">
				{(deployment.actorName !== "" || deployment.actorId !== "") && (
					<span>
						by {deploymentActor(deployment)}
						{deploymentMethod(deployment) !== "" && ` · ${deploymentMethod(deployment)}`}
					</span>
				)}
				{commit && (
					<span className="font-mono text-[12px] text-fg2" title={commit}>
						{shortSha(commit)}
					</span>
				)}
				{build?.gitCommitMessage && <span className="min-w-0 break-words text-fg2">{build.gitCommitMessage}</span>}
			</div>
			<ol aria-label="Deployment status" className="m-0 flex list-none p-0">
				{steps.map((step, index) => (
					<li
						key={step.label}
						className={cn(
							"relative flex min-w-0 flex-1 flex-col gap-0.5 pt-[18px] last:flex-none last:pr-2",
							step.state === "todo" && "text-fg3",
						)}
					>
						{index < steps.length - 1 && (
							<span
								aria-hidden
								className={cn(
									"absolute top-[5px] right-0 left-3 h-0.5 bg-line2",
									step.state === "done" && "bg-ok-glyph",
								)}
							/>
						)}
						<span
							aria-hidden
							className={cn(
								"absolute top-0 left-0 size-3 rounded-full border-2 border-line2 bg-background",
								step.state === "done" && "border-ok-glyph bg-ok-glyph",
								step.state === "active" && "border-info ring-4 ring-info/18",
								step.state === "failed" && "border-err bg-err",
							)}
						/>
						<span className={cn("text-[13px]", step.state === "failed" && "text-err")}>
							{step.label}
							<span className="sr-only">: {step.state}</span>
						</span>
						<span className="truncate pr-2 font-mono text-[11.5px] text-fg3" title={step.detail}>
							{step.detail}
						</span>
					</li>
				))}
			</ol>
			{controllerMessage !== "" && (
				<div
					className={cn(
						"flex flex-wrap items-baseline gap-2.5 rounded-lg bg-panel2 px-3 py-2.5 text-[13px]",
						failed && "bg-bad-bg text-bad-fg",
					)}
				>
					<span role="status" aria-live="polite" className="min-w-0 flex-1 basis-60 break-words">
						{controllerMessage}
					</span>
					{updated !== undefined && (
						<span className="text-[11.5px] text-fg3">updated {shortAgoLabel(updated, nowMs)}</span>
					)}
				</div>
			)}
			{status !== undefined && (
				<div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-[12.5px]">
					<span className="font-mono">{region.name}</span>
					<span aria-hidden className="flex max-w-full flex-wrap gap-1">
						{Array.from({ length: status.desiredReplicas }, (_, index) => (
							<i
								key={index}
								className={cn(
									"h-2 w-3.5 rounded-[2px] bg-line2",
									index < status.readyReplicas ? "bg-ok-glyph" : live && "bg-info/35 motion-safe:animate-pulse-soft",
								)}
							/>
						))}
					</span>
					<span className="text-fg2">
						{status.readyReplicas} of {status.desiredReplicas} ready
					</span>
					{reported !== undefined && (
						<span className="text-[11.5px] text-fg3">reported {shortAgoLabel(reported, nowMs)}</span>
					)}
					{status.message && status.message !== controllerMessage && (
						<span className={cn("min-w-0 break-words font-mono text-[11.5px] text-fg3", failed && "text-err")}>
							{status.message}
						</span>
					)}
				</div>
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
			{failed && (
				<div className="flex flex-wrap gap-2">
					{logsHref !== undefined && (
						<Button variant="outline" render={<Link to={logsHref} />}>
							<LogsIcon />
							View logs
						</Button>
					)}
					{previous !== undefined && (
						<Button
							variant="outline"
							disabled={!canWrite}
							onClick={() => {
								onRollback(previous, deployment);
							}}
						>
							<Undo2Icon />
							Roll back to {depTag(previous)}
						</Button>
					)}
				</div>
			)}
		</section>
	);
}

export function DeploymentProgress({
	regions,
	environmentName,
	logsHref,
	canWrite,
	onRollback,
}: {
	regions: RegionView[];
	environmentName: string;
	logsHref: string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const progress = progressDeployments(regions);
	const phaseOf = useDeploymentWatch(
		progress.filter(({ deployment }) => isInFlight(deployment.status)).map(({ deployment }) => deployment.id),
	);
	return progress.map(({ region, deployment, previous }) => {
		const watched = phaseOf(deployment.id);
		const watchedPhase = watched.phase;
		const watchIsNewer =
			watchedPhase !== undefined && watched.updatedMs !== undefined && watched.updatedMs >= tsMs(deployment.updatedAt);
		return (
			<ProgressPanel
				key={deployment.id}
				region={region}
				deployment={deployment}
				previous={previous}
				phase={watchIsNewer ? watchedPhase : deployment.status}
				message={watchIsNewer ? watched.message : deployment.message}
				updatedMs={watchIsNewer ? watched.updatedMs : undefined}
				environmentName={environmentName}
				logsHref={logsHref}
				canWrite={canWrite}
				onRollback={onRollback}
			/>
		);
	});
}
