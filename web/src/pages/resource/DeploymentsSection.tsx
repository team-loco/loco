import { deploymentMessage } from "@/hooks/deployment-watch";
import type { TimePreferences } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { skipToken, useQuery, useTransport } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { ChevronRightIcon, FileCodeIcon, HammerIcon, ScrollTextIcon, Undo2Icon } from "lucide-react";
import { useState } from "react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { Link } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Badge } from "@/components/design/Badge";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/design/Collapsible";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Section } from "@/components/design/Page";
import { Dialog, DialogBody, DialogContent } from "@/components/design/Dialog";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { SoonTag } from "@/components/design/SoonTag";
import { Spinner } from "@/components/design/Spinner";
import { Pager } from "@/components/design/Pager";
import { DeploymentActor } from "@/components/design/DeploymentOrigin";
import { DeploymentStatusBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Tabs, TabsContent, TabsCount, TabsList, TabsTrigger } from "@/components/design/Tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { deploymentImage } from "@/lib/deployment-utils";
import { getErrorMessage } from "@/lib/error-handler";
import { nextPage, pagedRows, type Paging } from "@/lib/paging";
import { agoLabel, maybeTsMs, tsMs } from "@/lib/time";
import { cn, formatShortId } from "@/lib/utils";
import { deploymentState } from "@/pages/dashboard/deploymentRows";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { formatCount } from "@/lib/format";

import { buildStatusStyle, deploymentBuildId, deploymentsByBuild, pageOf } from "./builds";
import { BuildDetail, BuildsTable, type BuildActor } from "./BuildsTable";
import { formatDuration, formatFullTime } from "./format";
import { canRollBackTo, depTag, PAGE_SIZES, previousDeployment, tookMs, type RegionView } from "./model";
import { ABSENT, specChanges } from "./spec";
import { useResourceBuilds } from "./useBuilds";

const BASE_COLUMNS = 7;
const DEFAULT_PAGE_SIZE = 10;
const BUILD_PAGE_SIZE = 5;
const BUILD_PAGE_SIZES = [BUILD_PAGE_SIZE];
const HISTS = ["deploys", "builds"] as const;
const histParser = parseAsStringLiteral(HISTS).withDefault("deploys").withOptions({ history: "replace" });

interface Row {
	dep: Deployment;
	region: RegionView;
	prev: Deployment | undefined;
	isCurrent: boolean;
}

function timeFact(ms: number | undefined, pref: TimePreferences): string {
	return ms === undefined ? "—" : formatFullTime(ms, false, pref);
}

function HistoryKept({ count, tip }: { count: number; tip: string }) {
	return (
		<Tooltip>
			<TooltipTrigger render={<span className="font-sans text-[12px] text-fg3" />}>
				<span aria-hidden className="mx-1.5">
					·
				</span>
				last {formatCount(count)} kept
			</TooltipTrigger>
			<TooltipContent className="max-w-[280px]">{tip}</TooltipContent>
		</Tooltip>
	);
}

function Detail({
	row,
	logsHref,
	onRollback,
	onViewSpec,
	onOpenBuild,
}: {
	row: Row;
	logsHref: string | undefined;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	onViewSpec: (dep: Deployment) => void;
	onOpenBuild: (buildId: string) => void;
}) {
	const timePref = useTimePreferences();
	const { dep, region, prev } = row;
	const buildId = deploymentBuildId(dep);
	const took = tookMs(dep);
	const changes = prev === undefined ? [] : specChanges(prev, dep);
	return (
		<div className="grid grid-cols-1 gap-6 pt-1.5 pb-5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] md:pl-9">
			<div className="flex min-w-0 flex-col gap-1.5">
				<span className="flex items-center gap-2 text-[12px] text-fg3">
					Rollout
					<SoonTag />
				</span>
				<dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[12.5px]">
					<dt className="text-fg3">Image</dt>
					<dd className="m-0 break-all font-mono">{deploymentImage(dep)}</dd>
					<dt className="text-fg3">Created</dt>
					<dd className="m-0 font-mono">{timeFact(maybeTsMs(dep.createdAt), timePref)}</dd>
					<dt className="text-fg3">Started</dt>
					<dd className="m-0 font-mono">{timeFact(maybeTsMs(dep.startedAt), timePref)}</dd>
					<dt className="text-fg3">Completed</dt>
					<dd className="m-0 font-mono">{timeFact(maybeTsMs(dep.completedAt), timePref)}</dd>
					<dt className="text-fg3">Took</dt>
					<dd className="m-0 font-mono">{took === undefined ? "—" : formatDuration(took)}</dd>
				</dl>
			</div>
			<div className="flex min-w-0 flex-col gap-1.5">
				<span className="text-[12px] text-fg3">Changes from previous</span>
				{prev === undefined && <span className="text-[12.5px] text-fg3">First deployment in {region.name}</span>}
				{prev !== undefined && changes.length === 0 && (
					<span className="text-[12.5px] text-fg3">Same spec as {formatShortId(prev.id)}</span>
				)}
				{changes.map((c) => (
					<div key={c.label} className="flex min-w-0 flex-wrap gap-x-2 font-mono text-[12px]">
						<span className="text-fg3">{c.label}</span>
						<span className="break-all text-bad-fg">{c.from ?? ABSENT}</span>
						<span className="text-fg3">→</span>
						<span className="break-all text-ok-fg">{c.to ?? ABSENT}</span>
					</div>
				))}
			</div>
			<div className="flex flex-wrap items-start gap-2 md:flex-col md:items-stretch">
				{canRollBackTo(dep, region) && (
					<Button
						variant="outline"
						size="sm"
						onClick={() => {
							onRollback(dep, region.current);
						}}
					>
						<Undo2Icon />
						Roll back to this
					</Button>
				)}
				{logsHref !== undefined && (
					<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
						<ScrollTextIcon />
						Logs
					</Button>
				)}
				<Button
					variant="outline"
					size="sm"
					onClick={() => {
						onViewSpec(dep);
					}}
				>
					<FileCodeIcon />
					View spec
				</Button>
				{buildId !== undefined && (
					<Button
						variant="outline"
						size="sm"
						onClick={() => {
							onOpenBuild(buildId);
						}}
					>
						<HammerIcon />
						Build <span className="font-mono">{formatShortId(buildId)}</span>
					</Button>
				)}
				{dep.commit !== undefined && (
					<CopyId value={dep.commit.sha} className="h-[30px] px-2.5 text-[12.5px] text-fg2">
						Copy SHA
					</CopyId>
				)}
			</div>
		</div>
	);
}

export function DeploymentsSection({
	resourceId,
	workspaceId,
	environmentId,
	regions,
	hasMore,
	isLoadingMore,
	historyError,
	onLoadMore,
	logsHref,
	buildLogsHref,
	canWrite,
	canReadWorkspace,
	onRollback,
	onViewSpec,
	nowMs,
}: {
	resourceId: string;
	workspaceId: string;
	environmentId: string | undefined;
	regions: RegionView[];
	hasMore: boolean;
	isLoadingMore: boolean;
	historyError: unknown;
	onLoadMore: () => Promise<boolean>;
	logsHref: string | undefined;
	buildLogsHref: (buildId: string) => string | undefined;
	canWrite: boolean;
	canReadWorkspace: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	onViewSpec: (dep: Deployment) => void;
	nowMs: number;
}) {
	const timePref = useTimePreferences();
	const transport = useTransport();
	const [pagination, setPagination] = useState<Paging>({ page: 0, size: DEFAULT_PAGE_SIZE });
	const [open, setOpen] = useState<string | null>(null);
	const [buildPaging, setBuildPaging] = useState<Paging>({ page: 0, size: BUILD_PAGE_SIZE });
	const [openBuild, setOpenBuild] = useState<string | null>(null);
	const builds = useResourceBuilds(resourceId, environmentId);
	const [selectedBuild, setSelectedBuild] = useState<string | null>(null);
	const selected = useQuery(getBuild, selectedBuild === null ? skipToken : { buildId: selectedBuild });
	const selectedStatus = selected.data?.build === undefined ? undefined : buildStatusStyle(selected.data.build.status);
	const { activeOrgId } = useOrgWorkspace();
	const limits = useOrgLimits(activeOrgId ?? "");
	const { data: membersRes } = useTanstackQuery({
		...workspaceMembersOptions(transport, workspaceId),
		enabled: canReadWorkspace,
	});
	const [hist, setHist] = useQueryState("hist", histParser);

	const all: Row[] = regions
		.flatMap((region) =>
			region.history.map((dep) => ({
				dep,
				region,
				prev: previousDeployment(region, dep),
				isCurrent: region.current?.id === dep.id,
			})),
		)
		.toSorted((a, b) => tsMs(b.dep.createdAt) - tsMs(a.dep.createdAt));
	const deployRows = pagedRows(all, pagination, setPagination, PAGE_SIZES);
	const multiRegion = regions.length > 1;
	const columns = BASE_COLUMNS + (multiRegion ? 1 : 0);

	const byBuild = deploymentsByBuild(all);
	const actors = new Map<string, BuildActor>(
		(membersRes?.members ?? []).map((m) => [
			m.userId,
			{ name: m.userName, email: m.userEmail, avatarUrl: m.userAvatarUrl },
		]),
	);
	const buildRows = pagedRows(builds.builds, buildPaging, setBuildPaging, BUILD_PAGE_SIZES);

	const goBuild = (buildId: string) => {
		const index = builds.builds.findIndex((build) => build.id === buildId);
		if (index !== -1) {
			void setHist("builds");
			setOpenBuild(buildId);
			setBuildPaging({ page: pageOf(index, BUILD_PAGE_SIZE), size: BUILD_PAGE_SIZE });
			return;
		}
		setSelectedBuild(buildId);
	};
	const goDeployment = (row: Row) => {
		void setHist("deploys");
		setOpen(row.dep.id);
		setPagination({ page: pageOf(all.indexOf(row), pagination.size), size: pagination.size });
	};
	const buildDeployment = (buildId: string) => {
		const row = byBuild.get(buildId);
		return row === undefined
			? undefined
			: {
					tag: depTag(row.dep),
					onOpen: () => {
						setSelectedBuild(null);
						goDeployment(row);
					},
				};
	};

	return (
		<Section>
			<Tabs
				value={hist}
				onValueChange={(next: string) => {
					void setHist(HISTS.find((h) => h === next) ?? "deploys");
				}}
			>
				<div className="px-5 pt-3">
					<TabsList variant="segmented" aria-label="History">
						<TabsTrigger value="deploys">
							Deployments{" "}
							<TabsCount>
								{all.length}
								{hasMore ? "+" : ""}
							</TabsCount>
						</TabsTrigger>
						<TabsTrigger value="builds">
							Builds{" "}
							<TabsCount>
								{builds.isLoading ? "—" : builds.builds.length}
								{builds.hasMore ? "+" : ""}
							</TabsCount>
						</TabsTrigger>
					</TabsList>
				</div>
				<TabsContent value="deploys" className="pt-1.5">
					<div aria-busy={isLoadingMore}>
						<Table inset className="min-w-[900px] table-fixed [--row-h:58px]">
							<TableHeader>
								<TableRow>
									<TableHead className="w-7" />
									<TableHead className="w-[130px]">Status</TableHead>
									<TableHead className="w-[180px]">Version</TableHead>
									<TableHead className="w-[200px]">Deployed by</TableHead>
									{multiRegion && <TableHead className="w-[110px]">Region</TableHead>}
									<TableHead>Message</TableHead>
									<TableHead className="w-[72px] text-right">Took</TableHead>
									<TableHead className="w-[100px] text-right">Time</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{deployRows.rows.length === 0 && (
									<TableRow>
										<TableCell colSpan={columns} className="h-24 text-center text-fg3">
											No deployments yet.
										</TableCell>
									</TableRow>
								)}
							</TableBody>
							{deployRows.rows.map((row) => {
								const { dep, isCurrent } = row;
								const expanded = open === dep.id;
								const took = tookMs(dep);
								const created = maybeTsMs(dep.createdAt);
								const toggle = () => {
									setOpen(expanded ? null : dep.id);
								};
								return (
									<Collapsible
										key={dep.id}
										open={expanded}
										onOpenChange={(next) => {
											setOpen(next ? dep.id : null);
										}}
										render={<TableBody />}
									>
										<TableRow className={expanded ? "border-b-0" : undefined} onClick={toggle}>
											<TableCell>
												<CollapsibleTrigger
													render={<Button variant="ghost" size="icon-xs" />}
													aria-label={`${expanded ? "Hide" : "Show"} details for ${formatShortId(dep.id)}`}
													onClick={(e) => {
														e.stopPropagation();
													}}
												>
													<ChevronRightIcon className={cn("transition-transform", expanded && "rotate-90")} />
												</CollapsibleTrigger>
											</TableCell>
											<TableCell>
												<DeploymentStatusBadge phase={dep.status} superseded={deploymentState(dep) === "superseded"} />
											</TableCell>
											<TableCell>
												<span className="flex min-w-0 items-center gap-1.5">
													<span className="truncate font-mono text-[12.5px] text-foreground">{depTag(dep)}</span>
													{isCurrent && <Badge tone="pill">current</Badge>}
												</span>
											</TableCell>
											<TableCell>
												<DeploymentActor deployment={dep} />
											</TableCell>
											{multiRegion && (
												<TableCell className="truncate font-mono text-[12px] text-fg2">{row.region.name}</TableCell>
											)}
											<TableCell className="max-w-0 truncate text-fg2" title={deploymentMessage(dep)}>
												{deploymentMessage(dep)}
											</TableCell>
											<TableCell className="text-right font-mono text-[12px] text-fg2">
												{took === undefined ? "—" : formatDuration(took)}
											</TableCell>
											<TableCell
												className="text-right font-mono text-[12px] text-fg2"
												title={created === undefined ? undefined : formatFullTime(created, false, timePref)}
											>
												{created === undefined ? "—" : agoLabel(created, nowMs)}
											</TableCell>
										</TableRow>
										<CollapsibleContent render={<TableRow />} className="data-closed:hidden">
											<TableCell colSpan={columns} className="h-auto p-0 whitespace-normal">
												<Detail
													row={row}
													logsHref={logsHref}
													onRollback={onRollback}
													onViewSpec={onViewSpec}
													onOpenBuild={goBuild}
												/>
											</TableCell>
										</CollapsibleContent>
									</Collapsible>
								);
							})}
						</Table>
					</div>
					<Pager
						{...deployRows.pager}
						label={
							<>
								{`${deployRows.pager.label}${hasMore ? "+" : ""}`}
								{limits !== undefined && (
									<HistoryKept
										count={limits.deploymentHistory}
										tip={`Loco keeps the last ${formatCount(limits.deploymentHistory)} deployments for each environment.`}
									/>
								)}
							</>
						}
						more={hasMore}
						onNext={nextPage(deployRows, isLoadingMore, hasMore, onLoadMore, setPagination)}
					/>
					{historyError !== null && (
						<div role="alert" className="px-5 pb-3 text-bad-fg">
							{getErrorMessage(historyError, "Could not load older deployments. Try Next again.")}
						</div>
					)}
				</TabsContent>
				<TabsContent value="builds" className="pt-1.5">
					<div aria-busy={builds.isLoading || builds.isLoadingMore}>
						<BuildsTable
							builds={buildRows.rows}
							actors={actors}
							workspaceId={workspaceId}
							nowMs={nowMs}
							canWrite={canWrite}
							openId={openBuild}
							onOpen={setOpenBuild}
							deploymentFor={buildDeployment}
							logsHref={buildLogsHref}
							onCanceled={builds.refresh}
						/>
					</div>
					<Pager
						{...buildRows.pager}
						label={
							<>
								{`${buildRows.pager.label}${builds.hasMore ? "+" : ""}`}
								{limits !== undefined && (
									<HistoryKept
										count={limits.buildHistory}
										tip={`Loco keeps the last ${formatCount(limits.buildHistory)} builds for each resource.`}
									/>
								)}
							</>
						}
						more={builds.hasMore}
						prevLabel="Newer builds"
						nextLabel="Older builds"
						onNext={nextPage(buildRows, builds.isLoadingMore, builds.hasMore, builds.loadMore, setBuildPaging)}
					/>
					{builds.error !== null && (
						<div role="alert" className="px-5 pb-3 text-bad-fg">
							{getErrorMessage(builds.error, "Could not load builds.")}
						</div>
					)}
				</TabsContent>
			</Tabs>
			<Dialog
				open={selectedBuild !== null}
				onOpenChange={(isOpen) => {
					if (!isOpen) setSelectedBuild(null);
				}}
			>
				<DialogContent size="wide" title={`Build ${selectedBuild === null ? "" : formatShortId(selectedBuild)}`}>
					<DialogBody>
						{selected.isPending && (
							<div role="status" className="flex items-center gap-2 py-5 text-fg3">
								<Spinner /> Loading build
							</div>
						)}
						{selected.error !== null && (
							<div role="alert" className="py-5 text-bad-fg">
								{getErrorMessage(selected.error, "Could not load build.")}
							</div>
						)}
						{selectedStatus !== undefined && (
							<div className="flex items-center gap-2 text-sm">
								<StatusGlyph glyph={selectedStatus.glyph} />
								{selectedStatus.label}
							</div>
						)}
						{selected.data?.build !== undefined && (
							<BuildDetail
								build={selected.data.build}
								workspaceId={workspaceId}
								canWrite={canWrite}
								deployment={buildDeployment(selected.data.build.id)}
								logsHref={buildLogsHref(selected.data.build.id)}
								onCanceled={() => {
									builds.refresh();
									void selected.refetch();
								}}
							/>
						)}
					</DialogBody>
				</DialogContent>
			</Dialog>
		</Section>
	);
}
