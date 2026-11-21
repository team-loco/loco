import { useEnvironments } from "@/hooks/useEnvironment";
import { useObservabilityMaxRange } from "@/hooks/useObservabilityMaxRange";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { useMutation } from "@connectrpc/connect-query";
import { RocketIcon, ScrollTextIcon, XIcon } from "lucide-react";
import { Fragment, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { cancelBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { BuildStatus, type Build } from "@gen/loco/build/v1/build_pb";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { CopyButton } from "@/components/design/CopyButton";
import { CellText, DataTable, expandColumn, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { StatusBadge } from "@/components/design/StatusBadge";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { agoLabel, maybeTsMs } from "@/lib/time";
import { cn, formatShortId } from "@/lib/utils";
import { logLevel } from "@/pages/observability/query";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import {
	buildImageLabel,
	buildLogSource,
	buildMessage,
	buildStarter,
	buildStatusStyle,
	buildTookMs,
	canCancelBuild,
	emptyBuildLogText,
	formatBuildDuration,
} from "./builds";
import { formatFullTime } from "./format";
import { BUILD_LOG_LIMIT, useBuildLog } from "./useBuilds";

export interface BuildActor {
	name: string;
	email: string;
	avatarUrl: string;
}

export interface BuildDeploymentLink {
	tag: string;
	onOpen: () => void;
}

function BuildLog({ build, workspaceId }: { build: Build; workspaceId: string }) {
	const maxRangeMs = useObservabilityMaxRange();
	const source = maxRangeMs === undefined ? { kind: "none" as const } : buildLogSource(build, maxRangeMs);
	const log = useBuildLog(workspaceId, build.id, source);
	if (log.error !== null) {
		return (
			<div
				role="alert"
				className="rounded-lg bg-panel px-4 py-4 text-[12.5px] text-bad-fg shadow-[inset_0_0_0_1px_var(--line)]"
			>
				{getErrorMessage(log.error, "Could not load build logs.")}
			</div>
		);
	}
	if (log.entries.length === 0) {
		return (
			<div
				aria-busy={log.isLoading || maxRangeMs === undefined}
				className="rounded-lg bg-panel px-4 py-4 font-mono text-[12.5px] text-fg3 shadow-[inset_0_0_0_1px_var(--line)]"
			>
				{log.isLoading || maxRangeMs === undefined ? "Loading logs…" : emptyBuildLogText(build.status)}
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-1.5">
			<pre
				tabIndex={0}
				className="m-0 flex max-h-[280px] flex-col-reverse overflow-auto rounded-lg bg-panel py-2.5 font-mono text-[12px] leading-[1.7] whitespace-pre-wrap shadow-[inset_0_0_0_1px_var(--line)] outline-none focus-visible:ring-2 focus-visible:ring-ring"
			>
				<span className="block">
					{log.entries.map((entry, i) => {
						const level = logLevel(entry.level);
						return (
							<span
								key={`${String(entry.timestamp?.seconds ?? "")}:${String(entry.timestamp?.nanos ?? "")}:${i.toString()}`}
								className={cn(
									"grid grid-cols-[40px_minmax(0,1fr)] gap-2.5 pr-3 [overflow-wrap:anywhere]",
									level === "error" && "bg-bad-bg text-bad-fg",
									level === "warn" && "text-warn-fg",
								)}
							>
								<span aria-hidden className="text-right text-fg3 select-none">
									{i + 1}
								</span>
								<span className="min-w-0">{entry.body}</span>
							</span>
						);
					})}
				</span>
			</pre>
			{log.truncated && (
				<span className="text-[12px] text-fg3">
					Showing the first {BUILD_LOG_LIMIT.toString()} lines. Open in Logs for the rest.
				</span>
			)}
		</div>
	);
}

export function BuildDetail({
	build,
	actors,
	workspaceId,
	deployment,
	logsHref,
	canWrite,
	onCanceled,
}: {
	build: Build;
	actors: ReadonlyMap<string, BuildActor>;
	workspaceId: string;
	deployment: BuildDeploymentLink | undefined;
	logsHref: string | undefined;
	canWrite: boolean;
	onCanceled: () => void;
}) {
	const [confirming, setConfirming] = useState(false);
	const cancel = useMutation(cancelBuild);

	const confirmCancel = () => {
		const short = formatShortId(build.id);
		cancel.mutate(
			{ buildId: build.id },
			{
				onSuccess: () => {
					toast.success(`Canceled build ${short}`);
					setConfirming(false);
					onCanceled();
				},
				onError: (err) => {
					toastConnectError(err, `Could not cancel build ${short}`);
				},
			},
		);
	};

	const { environments } = useEnvironments();
	const origin =
		environments.find((environment) => environment.id === build.originEnvironmentId)?.slug ??
		build.originEnvironmentId ??
		"Not recorded";
	const message = buildMessage(build);
	const starter = buildStarter(build, actors.get(build.createdBy));
	const starterFact = starter.detail === "" ? starter.name : `${starter.name} · ${starter.detail}`;
	const facts: [string, string][] = [
		["Build ID", build.id],
		["Started by", starterFact],
		["Origin environment", origin],
		["Dockerfile", build.dockerfilePath],
		["Context", build.context === "" ? "." : build.context],
		["Source", build.sourceType],
		["Image digest", build.imageDigest ?? "—"],
	];
	return (
		<>
			<div className="grid grid-cols-1 gap-6 pt-1 pb-5 lg:grid-cols-[minmax(0,1.7fr)_minmax(260px,1fr)] lg:pl-8">
				<div className="flex min-w-0 flex-col gap-2">
					{message !== undefined && (
						<div
							className={cn(
								"rounded-md px-2.5 py-2 text-[12.5px]",
								build.status === BuildStatus.FAILED ? "bg-bad-bg text-bad-fg" : "bg-panel2 text-fg2",
							)}
						>
							{message}
						</div>
					)}
					<BuildLog build={build} workspaceId={workspaceId} />
				</div>
				<div className="flex min-w-0 flex-col gap-3.5">
					<dl className="m-0 grid grid-cols-[90px_minmax(0,1fr)] gap-x-3 gap-y-[7px] text-[12.5px]">
						{facts.map(([k, v]) => (
							<Fragment key={k}>
								<dt className="text-fg3">{k}</dt>
								<dd className="m-0 font-mono text-[12px] [overflow-wrap:anywhere]">{v}</dd>
							</Fragment>
						))}
					</dl>
					<div className="flex flex-wrap gap-2">
						{deployment !== undefined && (
							<Button variant="outline" size="sm" onClick={deployment.onOpen}>
								<RocketIcon />
								Deployment <span className="font-mono">{deployment.tag}</span>
							</Button>
						)}
						{logsHref !== undefined && (
							<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
								<ScrollTextIcon />
								Open in Logs
							</Button>
						)}
						<CopyButton text={build.id} label="Copy ID" name={`Copy build ID ${formatShortId(build.id)}`} />
						{canWrite && canCancelBuild(build.status) && (
							<Button
								variant="destructive-outline"
								size="sm"
								onClick={() => {
									setConfirming(true);
								}}
							>
								<XIcon />
								Cancel build
							</Button>
						)}
					</div>
				</div>
			</div>
			<ConfirmDialog
				open={confirming}
				title={`Cancel build ${formatShortId(build.id)}?`}
				description="The build stops and produces no image."
				confirmLabel="Cancel build"
				pendingLabel="Canceling…"
				cancelLabel="Keep building"
				pending={cancel.isPending}
				onConfirm={confirmCancel}
				onClose={() => {
					setConfirming(false);
				}}
			/>
		</>
	);
}

/** How long ago `ms` was, with the full time on hover. */
export function TimeAgo({ ms, nowMs }: { ms: number | undefined; nowMs: number }) {
	const timePref = useTimePreferences();
	return (
		<span title={ms === undefined ? undefined : formatFullTime(ms, false, timePref)}>
			{ms === undefined ? "—" : agoLabel(ms, nowMs)}
		</span>
	);
}

export const TIME_CELL = "font-mono text-[12px] text-fg2";

function buildColumns(
	actors: ReadonlyMap<string, BuildActor>,
	nowMs: number,
	openId: string | null,
	onOpen: (id: string | null) => void,
): DataColumn<Build>[] {
	return [
		expandColumn(openId, onOpen, (build) => `build ${formatShortId(build.id)}`),
		{
			id: "status",
			header: "Status",
			size: 140,
			minSize: 90,
			cell: ({ row: { original: build } }) => <StatusBadge {...buildStatusStyle(build.status)} />,
		},
		{
			id: "build",
			header: "Build",
			size: 100,
			minSize: 80,
			meta: { cellClassName: "font-mono text-[12.5px]" },
			cell: ({ row: { original: build } }) => formatShortId(build.id),
		},
		{
			id: "actor",
			header: "Started by",
			size: 220,
			minSize: 90,
			cell: ({ row: { original: build } }) => {
				const actor = actors.get(build.createdBy);
				const starter = buildStarter(build, actor);
				return (
					<span className="flex min-w-0 items-center gap-2">
						<MemberAvatar
							name={actor?.name ?? starter.name}
							email={actor?.email ?? ""}
							avatarUrl={actor?.avatarUrl ?? ""}
							className="size-[22px]"
						/>
						<span className="flex min-w-0 flex-col">
							<CellText text={starter.name} />
							{starter.detail !== "" && <CellText text={starter.detail} className="text-[11.5px] text-fg3" />}
						</span>
					</span>
				);
			},
		},
		{
			id: "image",
			header: "Image",
			size: 160,
			minSize: 90,
			meta: { grow: true },
			cell: ({ row: { original: build } }) => {
				const image = buildImageLabel(build);
				return <CellText text={image.text} className={cn("font-mono text-[12px]", image.muted && "text-fg3")} />;
			},
		},
		{
			id: "took",
			header: "Took",
			size: 72,
			minSize: 72,
			meta: { align: "end", cellClassName: TIME_CELL },
			cell: ({ row: { original: build } }) => {
				const took = buildTookMs(build);
				return took === undefined ? "—" : formatBuildDuration(took);
			},
		},
		{
			id: "time",
			header: "Time",
			size: 100,
			minSize: 72,
			meta: { align: "end", cellClassName: TIME_CELL },
			cell: ({ row: { original: build } }) => <TimeAgo ms={maybeTsMs(build.createdAt)} nowMs={nowMs} />,
		},
	];
}

export function BuildsTable({
	builds,
	actors,
	workspaceId,
	nowMs,
	canWrite,
	openId,
	onOpen,
	deploymentFor,
	logsHref,
	onCanceled,
	emptyText,
}: {
	builds: Build[];
	actors: ReadonlyMap<string, BuildActor>;
	workspaceId: string;
	nowMs: number;
	canWrite: boolean;
	openId: string | null;
	onOpen: (id: string | null) => void;
	deploymentFor: (buildId: string) => BuildDeploymentLink | undefined;
	logsHref: (buildId: string) => string | undefined;
	onCanceled: () => void;
	emptyText: string;
}) {
	const table = useDataTable("builds", {
		columns: buildColumns(actors, nowMs, openId, onOpen),
		data: builds,
		getRowId: (build) => build.id,
	});
	return (
		<DataTable
			table={table}
			label="Builds"
			empty={<div className="flex h-24 items-center justify-center text-fg3">{emptyText}</div>}
			onRowClick={(row) => {
				onOpen(openId === row.id ? null : row.id);
			}}
			renderExpanded={(row) =>
				openId === row.id ? (
					<BuildDetail
						build={row.original}
						actors={actors}
						workspaceId={workspaceId}
						deployment={deploymentFor(row.id)}
						logsHref={logsHref(row.id)}
						canWrite={canWrite}
						onCanceled={onCanceled}
					/>
				) : null
			}
		/>
	);
}
