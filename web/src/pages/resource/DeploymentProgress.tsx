import { useQuery } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { DeploymentStatus, type Activity, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
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
import { tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { formatBuildDuration, isBuildFinished } from "./builds";
import {
	deploymentElapsedMs,
	deploymentStatusLabel,
	progressDeployments,
	showDeploymentLogs,
} from "./deployment-progress";
import { DeploymentRollout } from "./DeploymentRollout";
import { depImage, depTag, type RegionView } from "./model";

function ProgressPanel({
	region,
	deployment,
	previous,
	activity,
	connectionError,
	environmentName,
	logsHref,
	canWrite,
	onRollback,
}: {
	region: RegionView;
	deployment: Deployment;
	previous: Deployment | undefined;
	activity: readonly Activity[];
	connectionError: string | undefined;
	environmentName: string;
	logsHref: string | undefined;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const titleId = useId();
	const phase = deployment.status;
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
	const failed = phase === DeploymentStatus.FAILED;
	const canceled = phase === DeploymentStatus.CANCELED;
	const title = deploymentStatusLabel(deployment);
	const elapsed = deploymentElapsedMs(deployment, nowMs);
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
				<StatusGlyph
					glyph={
						failed
							? "err"
							: canceled || phase === DeploymentStatus.SUPERSEDED
								? "off"
								: live
									? "spin"
									: deployment.problem !== undefined
										? "warn"
										: "ok"
					}
					className="size-4 shrink-0"
				/>
				<div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2.5 gap-y-1">
					<h2 id={titleId} className={cn("m-0 break-words font-mono text-[15px] font-medium", failed && "text-err")}>
						{title}
					</h2>
					<span className="text-[13px] text-fg3">
						to {environmentName === "" ? region.name : `${environmentName} · ${region.name}`}
					</span>
				</div>
				{elapsed !== undefined && (
					<span className="col-start-2 row-start-2 flex flex-col text-[11.5px] text-fg3 sm:col-start-3 sm:row-start-1 sm:items-end">
						<span>Elapsed</span>
						<span className="font-mono text-[14px] text-foreground">{formatBuildDuration(elapsed)}</span>
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
				{!commit && <span className="min-w-0 break-all font-mono text-[12px] text-fg2">{depImage(deployment)}</span>}
				{commit && (
					<span className="font-mono text-[12px] text-fg2" title={commit}>
						{shortSha(commit)}
					</span>
				)}
				{build?.gitCommitMessage && <span className="min-w-0 break-words text-fg2">{build.gitCommitMessage}</span>}
			</div>
			{connectionError !== undefined && (
				<p role="status" className="m-0 text-[13px] text-warn-fg">
					Disconnected. Reconnecting… {connectionError}
				</p>
			)}
			<DeploymentRollout deployment={deployment} activity={activity} logsHref={logsHref} />
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
					{showDeploymentLogs(logsHref, deployment) && logsHref !== undefined && (
						<Button variant="outline" nativeButton={false} render={<Link to={logsHref} />}>
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
	const watchOf = useDeploymentWatch(progress.map(({ deployment }) => deployment.id));
	return progress.map(({ region, deployment, previous }) => {
		const watched = watchOf(deployment.id);
		const current =
			watched.deployment !== undefined && tsMs(watched.deployment.updatedAt) >= tsMs(deployment.updatedAt)
				? watched.deployment
				: deployment;
		return (
			<ProgressPanel
				key={deployment.id}
				region={region}
				deployment={current}
				previous={previous}
				activity={watched.activity}
				connectionError={watched.error}
				environmentName={environmentName}
				logsHref={logsHref}
				canWrite={canWrite}
				onRollback={onRollback}
			/>
		);
	});
}
