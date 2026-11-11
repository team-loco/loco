import { useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { ChevronRightIcon, FileCodeIcon, HammerIcon, ScrollTextIcon, Undo2Icon } from "lucide-react";
import { Fragment, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SoonTag } from "@/components/design/SoonTag";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Tabs, TabsContent, TabsCount, TabsList, TabsTrigger } from "@/components/design/Tabs";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { getErrorMessage } from "@/lib/error-handler";
import { nextPage, pagedRows, type Paging } from "@/lib/paging";
import { agoLabel, maybeTsMs, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { deploymentState } from "@/pages/dashboard/deploymentRows";

import { deploymentBuildId, deploymentsByBuild, pageOf } from "./builds";
import { BuildsTable, type BuildActor } from "./BuildsTable";
import { formatDuration, formatFullTime, shortId } from "./format";
import { canRollBackTo, depTag, PAGE_SIZES, previousDeployment, tookMs, type RegionView } from "./model";
import { ABSENT, specChanges } from "./spec";
import { useResourceBuilds } from "./useBuilds";

const BASE_COLUMNS = 7;
const DEFAULT_PAGE_SIZE = 10;
const BUILD_PAGE_SIZE = 5;
const BUILD_PAGE_SIZES = [BUILD_PAGE_SIZE];
const HIST_PARAM = "hist";

type Hist = "deploys" | "builds";

function parseHist(value: string | null): Hist {
	return value === "builds" ? "builds" : "deploys";
}

interface Row {
	dep: Deployment;
	region: RegionView;
	prev: Deployment | undefined;
	isCurrent: boolean;
}

function timeFact(ms: number | undefined): string {
	return ms === undefined ? "—" : formatFullTime(ms);
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
	const { dep, region, prev } = row;
	const buildId = deploymentBuildId(dep);
	const took = tookMs(dep);
	const changes = prev === undefined ? [] : specChanges(prev, dep);
	return (
		<div className="grid grid-cols-1 gap-6 pt-1.5 pb-5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] md:pl-8">
			<dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[12.5px]">
				<dt className="text-fg3">Created</dt>
				<dd className="m-0 font-mono">{timeFact(maybeTsMs(dep.createdAt))}</dd>
				<dt className="text-fg3">Started</dt>
				<dd className="m-0 font-mono">{timeFact(maybeTsMs(dep.startedAt))}</dd>
				<dt className="text-fg3">Completed</dt>
				<dd className="m-0 font-mono">{timeFact(maybeTsMs(dep.completedAt))}</dd>
				<dt className="text-fg3">Took</dt>
				<dd className="m-0 font-mono">{took === undefined ? "—" : formatDuration(took)}</dd>
			</dl>
			<div className="flex min-w-0 flex-col gap-1.5">
				<span className="text-[12px] text-fg3">Changes from previous</span>
				{prev === undefined && <span className="text-[12.5px] text-fg3">First deployment in {region.name}</span>}
				{prev !== undefined && changes.length === 0 && (
					<span className="text-[12.5px] text-fg3">Same spec as {shortId(prev.id)}</span>
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
						Build <span className="font-mono">{shortId(buildId)}</span>
					</Button>
				)}
				<CopyId value={dep.id} className="h-[30px] px-2.5 text-[12.5px] text-fg2">
					Copy ID
				</CopyId>
			</div>
		</div>
	);
}

export function DeploymentsSection({
	resourceId,
	workspaceId,
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
	const transport = useTransport();
	const [params, setParams] = useSearchParams();
	const [pagination, setPagination] = useState<Paging>({ page: 0, size: DEFAULT_PAGE_SIZE });
	const [open, setOpen] = useState<string | null>(null);
	const [buildPaging, setBuildPaging] = useState<Paging>({ page: 0, size: BUILD_PAGE_SIZE });
	const [openBuild, setOpenBuild] = useState<string | null>(null);
	const builds = useResourceBuilds(resourceId);
	const { data: membersRes } = useTanstackQuery({
		...workspaceMembersOptions(transport, workspaceId),
		enabled: canReadWorkspace,
	});
	const hist = parseHist(params.get(HIST_PARAM));

	const setHist = (next: Hist) => {
		const p = new URLSearchParams(params);
		if (next === "builds") p.set(HIST_PARAM, next);
		else p.delete(HIST_PARAM);
		setParams(p, { replace: true });
	};

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
		const index = builds.builds.findIndex((b) => b.id === buildId);
		setHist("builds");
		setOpenBuild(buildId);
		if (index !== -1) setBuildPaging({ page: pageOf(index, BUILD_PAGE_SIZE), size: BUILD_PAGE_SIZE });
	};
	const goDeployment = (row: Row) => {
		setHist("deploys");
		setOpen(row.dep.id);
		setPagination({ page: pageOf(all.indexOf(row), pagination.size), size: pagination.size });
	};

	return (
		<Section>
			<Tabs
				value={hist}
				onValueChange={(next: string) => {
					setHist(parseHist(next));
				}}
			>
				<div className="px-5 pt-3">
					<TabsList aria-label="History">
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
						<Table className="min-w-[860px]">
							<TableHeader>
								<TableRow>
									<TableHead className="w-6" />
									<TableHead className="w-[130px]">Status</TableHead>
									<TableHead className="w-[170px]">Version</TableHead>
									{multiRegion && <TableHead className="w-[110px]">Region</TableHead>}
									<TableHead className="w-[130px]">
										<span className="flex items-center gap-2">
											Deployed by
											<SoonTag />
										</span>
									</TableHead>
									<TableHead>Message</TableHead>
									<TableHead className="w-[72px] text-right">Took</TableHead>
									<TableHead className="w-[100px] text-right">When</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{deployRows.rows.length === 0 && (
									<TableRow>
										<TableCell colSpan={columns} className="text-fg3">
											No deployments yet.
										</TableCell>
									</TableRow>
								)}
								{deployRows.rows.map((row) => {
									const { dep, isCurrent } = row;
									const expanded = open === dep.id;
									const took = tookMs(dep);
									const created = maybeTsMs(dep.createdAt);
									const toggle = () => {
										setOpen(expanded ? null : dep.id);
									};
									return (
										<Fragment key={dep.id}>
											<TableRow
												className={cn("cursor-pointer hover:bg-panel2", expanded && "border-b-0")}
												onClick={toggle}
											>
												<TableCell>
													<Button
														variant="ghost"
														size="icon-xs"
														aria-expanded={expanded}
														aria-label={`${expanded ? "Hide" : "Show"} details for ${shortId(dep.id)}`}
														onClick={(e) => {
															e.stopPropagation();
															toggle();
														}}
													>
														<ChevronRightIcon className={cn("transition-transform", expanded && "rotate-90")} />
													</Button>
												</TableCell>
												<TableCell>
													<DeploymentPhaseBadge phase={dep.status} superseded={deploymentState(dep) === "superseded"} />
												</TableCell>
												<TableCell>
													<span className="flex items-center gap-2">
														<span className="truncate font-mono text-[12.5px]" title={depTag(dep)}>
															{depTag(dep)}
														</span>
														{isCurrent && <Badge tone="pill">current</Badge>}
													</span>
												</TableCell>
												{multiRegion && (
													<TableCell className="truncate font-mono text-[12px] text-fg2">{row.region.name}</TableCell>
												)}
												<TableCell className="text-fg4">—</TableCell>
												<TableCell className="max-w-0 truncate text-fg2" title={dep.message}>
													{dep.message}
												</TableCell>
												<TableCell className="text-right font-mono text-[12px] text-fg2">
													{took === undefined ? "—" : formatDuration(took)}
												</TableCell>
												<TableCell
													className="text-right font-mono text-[12px] text-fg2"
													title={created === undefined ? undefined : formatFullTime(created)}
												>
													{created === undefined ? "—" : agoLabel(created, nowMs)}
												</TableCell>
											</TableRow>
											{expanded && (
												<TableRow>
													<TableCell colSpan={columns} className="h-auto p-0 whitespace-normal">
														<Detail
															row={row}
															logsHref={logsHref}
															onRollback={onRollback}
															onViewSpec={onViewSpec}
															onOpenBuild={goBuild}
														/>
													</TableCell>
												</TableRow>
											)}
										</Fragment>
									);
								})}
							</TableBody>
						</Table>
					</div>
					<Pager
						{...deployRows.pager}
						label={`${deployRows.pager.label}${hasMore ? "+" : ""}`}
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
							deploymentFor={(buildId) => {
								const row = byBuild.get(buildId);
								return row === undefined
									? undefined
									: {
											tag: depTag(row.dep),
											onOpen: () => {
												goDeployment(row);
											},
										};
							}}
							logsHref={buildLogsHref}
							onCanceled={builds.refresh}
						/>
					</div>
					<Pager
						label={`${buildRows.pager.label}${builds.hasMore ? "+" : ""}`}
						prevLabel="Newer builds"
						nextLabel="Older builds"
						onPrev={buildRows.pager.onPrev}
						onNext={nextPage(buildRows, builds.isLoadingMore, builds.hasMore, builds.loadMore, setBuildPaging)}
					/>
					{builds.error !== null && (
						<div role="alert" className="px-5 pb-3 text-bad-fg">
							{getErrorMessage(builds.error, "Could not load builds.")}
						</div>
					)}
				</TabsContent>
			</Tabs>
		</Section>
	);
}
