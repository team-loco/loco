import { useMutation } from "@connectrpc/connect-query";
import { ChevronRightIcon, RocketIcon, ScrollTextIcon, XIcon } from "lucide-react";
import { Fragment, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { cancelBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { BuildStatus, type Build } from "@gen/loco/build/v1/build_pb";

import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/design/Collapsible";
import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { CopyButton } from "@/components/design/CopyButton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { agoLabel, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { levelOf } from "@/pages/observability/query";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import {
	buildImageLabel,
	buildLogSource,
	buildMessage,
	buildStatusStyle,
	buildTookMs,
	canCancelBuild,
	emptyBuildLogText,
	formatBuildDuration,
} from "./builds";
import { formatFullTime, shortId } from "./format";
import { BUILD_LOG_LIMIT, useBuildLog } from "./useBuilds";

const COLUMNS = 7;

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
	const source = buildLogSource(build);
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
				aria-busy={log.isLoading}
				className="rounded-lg bg-panel px-4 py-4 font-mono text-[12.5px] text-fg3 shadow-[inset_0_0_0_1px_var(--line)]"
			>
				{log.isLoading ? "Loading logs…" : emptyBuildLogText(build.status)}
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
						const level = levelOf(entry.severity);
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

function Detail({
	build,
	workspaceId,
	deployment,
	logsHref,
	canWrite,
	onCancel,
}: {
	build: Build;
	workspaceId: string;
	deployment: BuildDeploymentLink | undefined;
	logsHref: string | undefined;
	canWrite: boolean;
	onCancel: () => void;
}) {
	const message = buildMessage(build);
	const facts: [string, string][] = [
		["Build ID", build.id],
		["Dockerfile", build.dockerfilePath],
		["Context", build.context === "" ? "." : build.context],
		["Source", build.sourceType],
		["Image digest", build.imageDigest ?? "—"],
	];
	return (
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
					<CopyButton text={build.id} label="Copy ID" name={`Copy build ID ${shortId(build.id)}`} />
					{canWrite && canCancelBuild(build.status) && (
						<Button variant="destructive-outline" size="sm" onClick={onCancel}>
							<XIcon />
							Cancel build
						</Button>
					)}
				</div>
			</div>
		</div>
	);
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
}) {
	const [confirming, setConfirming] = useState<Build | null>(null);
	const cancel = useMutation(cancelBuild);

	const confirmCancel = () => {
		if (confirming === null) return;
		const short = shortId(confirming.id);
		cancel.mutate(
			{ buildId: confirming.id },
			{
				onSuccess: () => {
					toast.success(`Canceled build ${short}`);
					setConfirming(null);
					onCanceled();
				},
				onError: (err) => {
					toastConnectError(err, `Could not cancel build ${short}`);
				},
			},
		);
	};

	return (
		<>
			<Table className="min-w-[860px]">
				<TableHeader>
					<TableRow>
						<TableHead className="w-6" />
						<TableHead className="w-[140px]">Status</TableHead>
						<TableHead className="w-[100px]">Build</TableHead>
						<TableHead className="w-[220px]">Started by</TableHead>
						<TableHead>Image</TableHead>
						<TableHead className="w-[72px] text-right">Took</TableHead>
						<TableHead className="w-[100px] text-right">When</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{builds.length === 0 && (
						<TableRow>
							<TableCell colSpan={COLUMNS} className="text-fg3">
								No builds yet.
							</TableCell>
						</TableRow>
					)}
				</TableBody>
				{builds.map((build) => {
					const expanded = openId === build.id;
					const style = buildStatusStyle(build.status);
					const actor = actors.get(build.createdBy);
					const actorName = actor === undefined ? shortId(build.createdBy) : actor.name || actor.email;
					const image = buildImageLabel(build);
					const took = buildTookMs(build);
					const created = maybeTsMs(build.createdAt);
					const toggle = () => {
						onOpen(expanded ? null : build.id);
					};
					return (
						<Collapsible
							key={build.id}
							open={expanded}
							onOpenChange={(next) => {
								onOpen(next ? build.id : null);
							}}
							render={<TableBody />}
						>
							<TableRow className={cn("cursor-pointer hover:bg-panel2", expanded && "border-b-0")} onClick={toggle}>
								<TableCell>
									<CollapsibleTrigger
										render={<Button variant="ghost" size="icon-xs" />}
										aria-label={`${expanded ? "Hide" : "Show"} build ${shortId(build.id)}`}
										onClick={(e) => {
											e.stopPropagation();
										}}
									>
										<ChevronRightIcon className={cn("transition-transform", expanded && "rotate-90")} />
									</CollapsibleTrigger>
								</TableCell>
								<TableCell>
									<span className="inline-flex items-center gap-2 text-[13.5px] whitespace-nowrap">
										<StatusGlyph glyph={style.glyph} />
										{style.label}
									</span>
								</TableCell>
								<TableCell className="font-mono text-[12.5px]">{shortId(build.id)}</TableCell>
								<TableCell>
									<span className="flex min-w-0 items-center gap-2">
										<MemberAvatar
											name={actor?.name ?? actorName}
											email={actor?.email ?? ""}
											avatarUrl={actor?.avatarUrl ?? ""}
											className="size-[22px]"
										/>
										<span className="truncate">{actorName}</span>
									</span>
								</TableCell>
								<TableCell className={cn("truncate font-mono text-[12px]", image.muted && "text-fg3")}>
									{image.text}
								</TableCell>
								<TableCell className="text-right font-mono text-[12px] text-fg2">
									{took === undefined ? "—" : formatBuildDuration(took)}
								</TableCell>
								<TableCell
									className="text-right font-mono text-[12px] text-fg2"
									title={created === undefined ? undefined : formatFullTime(created)}
								>
									{created === undefined ? "—" : agoLabel(created, nowMs)}
								</TableCell>
							</TableRow>
							<CollapsibleContent render={<TableRow />}>
								<TableCell colSpan={COLUMNS} className="h-auto p-0 whitespace-normal">
									<Detail
										build={build}
										workspaceId={workspaceId}
										deployment={deploymentFor(build.id)}
										logsHref={logsHref(build.id)}
										canWrite={canWrite}
										onCancel={() => {
											setConfirming(build);
										}}
									/>
								</TableCell>
							</CollapsibleContent>
						</Collapsible>
					);
				})}
			</Table>
			<ConfirmDialog
				open={confirming !== null}
				title={`Cancel build ${confirming === null ? "" : shortId(confirming.id)}?`}
				description="The build stops and produces no image."
				confirmLabel="Cancel build"
				pendingLabel="Canceling…"
				cancelLabel="Keep building"
				pending={cancel.isPending}
				onConfirm={confirmCancel}
				onClose={() => {
					setConfirming(null);
				}}
			/>
		</>
	);
}
