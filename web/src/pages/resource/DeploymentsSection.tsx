import { deploymentMessage } from "@/hooks/deployment-watch";
import { skipToken, useQuery, useTransport } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { FileCodeIcon, HammerIcon, ScrollTextIcon, Undo2Icon } from "lucide-react";
import { useState } from "react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { Link } from "react-router";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { CellText, DataTable, expandColumn, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Section } from "@/components/design/Page";
import { Dialog, DialogBody, DialogContent } from "@/components/design/Dialog";
import { Spinner } from "@/components/design/Spinner";
import { Pager } from "@/components/design/Pager";
import { DeploymentActor } from "@/components/design/DeploymentOrigin";
import { DeploymentStatusBadge, StatusBadge } from "@/components/design/StatusBadge";
import { PlacementChips } from "@/components/design/PlacementChips";
import { Tabs, TabsContent, TabsCount, TabsList, TabsTrigger } from "@/components/design/Tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { tookLabel } from "@/lib/activity-deploys";
import { deploymentProblem, isInFlight } from "@/lib/deployment-utils";
import { getErrorMessage } from "@/lib/error-handler";
import {
	formatTotal,
	nextPage,
	pagedRows,
	pageTokensParser,
	tokenPageLabel,
	tokenPager,
	type Paging,
} from "@/lib/paging";
import { maybeTsMs, readyMs, tsMs } from "@/lib/time";
import { formatShortId } from "@/lib/utils";
import { deploymentState } from "@/pages/dashboard/deploymentRows";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { formatCount } from "@/lib/format";

import { buildStatusStyle, deploymentBuildId, deploymentsByBuild, pageOf } from "./builds";
import { BuildDetail, BuildsTable, TIME_CELL, TimeAgo, type BuildActor } from "./BuildsTable";
import { StepRail } from "./DeploymentProgress";
import { rolloutSteps } from "./deployment-progress";
import { canRollBackTo, depTag, PAGE_SIZES, previousDeployment, tookMs, type RegionView } from "./model";
import { ABSENT, specChanges } from "./spec";
import { useResourceBuilds } from "./useBuilds";

const DEFAULT_PAGE_SIZE = 10;
const BUILD_PAGE_SIZE = 5;
const BUILD_PAGE_SIZES = [BUILD_PAGE_SIZE];
const HISTS = ["deploys", "builds"] as const;
const histParser = parseAsStringLiteral(HISTS).withDefault("deploys").withOptions({ history: "replace" });
const buildPagesParser = pageTokensParser.withOptions({ history: "replace" });

interface Row {
	dep: Deployment;
	region: RegionView;
	prev: Deployment | undefined;
}

/** The regions of one deployment, one row per region. */
interface RolloutRow {
	key: string;
	rows: [Row, ...Row[]];
}

/** The member a rollout's status shows: one still in flight, else one that failed, else the newest. */
function leadRow(rows: readonly [Row, ...Row[]]): Row {
	return (
		rows.find((r) => isInFlight(r.dep.status)) ??
		rows.find((r) => r.dep.status === DeploymentStatus.FAILED || r.dep.status === DeploymentStatus.CRASHED) ??
		rows[0]
	);
}

function historyMessage(dep: Deployment): string {
	return deploymentMessage(dep) || (dep.commit?.message ?? "");
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
	const { dep, region, prev } = row;
	const buildId = deploymentBuildId(dep);
	const took = tookMs(dep);
	const changes = prev === undefined ? [] : specChanges(prev, dep);
	return (
		<div className="grid grid-cols-1 gap-6 pt-1.5 pb-5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] md:pl-9">
			<div className="flex min-w-0 flex-col gap-1.5">
				<span className="text-[12px] text-fg3">Rollout</span>
				<StepRail
					steps={rolloutSteps(dep, buildId !== undefined)}
					note={(step) => {
						if (step.state === "failed") return deploymentProblem(dep)?.message ?? "";
						if (step.state === "stopped") return "Canceled";
						return step.key === "deploy" && step.state === "done" && took !== undefined
							? `took ${tookLabel(took)}`
							: "";
					}}
				/>
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
				{dep.commit !== undefined && (
					<CopyId value={dep.commit.sha} className="h-[30px] px-2.5 text-[12.5px] text-fg2">
						Copy SHA
					</CopyId>
				)}
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
			</div>
		</div>
	);
}

function rolloutColumns(
	nowMs: number,
	open: string | null,
	setOpen: (key: string | null) => void,
): DataColumn<RolloutRow>[] {
	return [
		expandColumn(open, setOpen, (rollout) => `details for ${formatShortId(rollout.rows[0].dep.id)}`),
		{
			id: "status",
			header: "Status",
			size: 110,
			minSize: 90,
			meta: { fit: true, fitMax: 140 },
			cell: ({ row: { original: rollout } }) => {
				const { dep } = leadRow(rollout.rows);
				return <DeploymentStatusBadge phase={dep.status} superseded={deploymentState(dep) === "superseded"} />;
			},
		},
		{
			id: "version",
			header: "Version",
			size: 130,
			minSize: 90,
			meta: { fit: true, fitMax: 160 },
			cell: ({ row: { original: rollout } }) => (
				<CellText text={depTag(leadRow(rollout.rows).dep)} className="font-mono text-[12.5px] text-foreground" />
			),
		},
		{
			id: "actor",
			header: "Deployed by",
			size: 120,
			minSize: 90,
			cell: ({ row: { original: rollout } }) => <DeploymentActor deployment={leadRow(rollout.rows).dep} />,
		},
		{
			id: "region",
			header: "Region",
			size: 120,
			minSize: 100,
			meta: { fit: true, fitMax: 160 },
			cell: ({ row: { original: rollout } }) => (
				<PlacementChips placements={leadRow(rollout.rows).dep.placements} className="flex-col items-start" />
			),
		},
		{
			id: "message",
			header: "Message",
			size: 80,
			minSize: 72,
			meta: { grow: true },
			cell: ({ row: { original: rollout } }) => (
				<CellText text={historyMessage(leadRow(rollout.rows).dep)} className="text-fg2" />
			),
		},
		{
			id: "took",
			header: "Took",
			size: 88,
			minSize: 88,
			meta: { align: "end", cellClassName: TIME_CELL },
			cell: ({ row: { original: rollout } }) => {
				const tooks = rollout.rows.flatMap((r) => tookMs(r.dep) ?? []);
				return tooks.length === rollout.rows.length ? tookLabel(Math.max(...tooks)) : "—";
			},
		},
		{
			id: "started",
			header: "Started",
			size: 84,
			minSize: 72,
			meta: { align: "end", cellClassName: TIME_CELL },
			cell: ({ row: { original: rollout } }) => <TimeAgo ms={maybeTsMs(rollout.rows[0].dep.createdAt)} nowMs={nowMs} />,
		},
		{
			id: "deployed",
			header: "Deployed",
			size: 84,
			minSize: 72,
			meta: { align: "end", cellClassName: TIME_CELL },
			cell: ({ row: { original: rollout } }) => <TimeAgo ms={readyMs(rollout.rows[0].dep)} nowMs={nowMs} />,
		},
	];
}

export function DeploymentsSection({
	resourceId,
	resourceName,
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
	resourceName: string;
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
	const transport = useTransport();
	const [pagination, setPagination] = useState<Paging>({ page: 0, size: DEFAULT_PAGE_SIZE });
	const [open, setOpen] = useState<string | null>(null);
	const [openBuild, setOpenBuild] = useState<string | null>(null);
	const [buildPages, setBuildPages] = useQueryState("builds", buildPagesParser);
	const builds = useResourceBuilds(resourceId, environmentId, BUILD_PAGE_SIZE, buildPages);
	const buildPager = tokenPager(buildPages, builds.nextPageToken, (pages) => {
		void setBuildPages(pages);
	});
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
			})),
		)
		.toSorted((a, b) => tsMs(b.dep.createdAt) - tsMs(a.dep.createdAt));
	const rollouts: RolloutRow[] = [...Map.groupBy(all, (row) => row.dep.id).values()].flatMap(([first, ...rest]) =>
		first === undefined ? [] : [{ key: first.dep.id, rows: [first, ...rest] }],
	);
	const deployRows = pagedRows(rollouts, pagination, setPagination, PAGE_SIZES);
	const deployTable = useDataTable("deployments", {
		columns: rolloutColumns(nowMs, open, setOpen),
		data: deployRows.rows,
		getRowId: (rollout) => rollout.key,
		state: { columnVisibility: { region: regions.length > 1 } },
	});

	const byBuild = deploymentsByBuild(all);
	const actors = new Map<string, BuildActor>(
		(membersRes?.members ?? []).map((m) => [
			m.userId,
			{ name: m.userName, email: m.userEmail, avatarUrl: m.userAvatarUrl },
		]),
	);
	const running = regions.flatMap((r) => (r.current === undefined ? [] : [r.current]));
	const imageOnly = running.length > 0 && running.every((dep) => deploymentBuildId(dep) === undefined);

	const goBuild = (buildId: string) => {
		if (builds.builds.some((build) => build.id === buildId)) {
			void setHist("builds");
			setOpenBuild(buildId);
			return;
		}
		setSelectedBuild(buildId);
	};
	const goDeployment = (row: Row) => {
		const index = rollouts.findIndex((rollout) => rollout.rows.includes(row));
		void setHist("deploys");
		setOpen(rollouts[index]?.key ?? null);
		setPagination({ page: pageOf(index, pagination.size), size: pagination.size });
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
								{rollouts.length}
								{hasMore ? "+" : ""}
							</TabsCount>
						</TabsTrigger>
						<TabsTrigger value="builds">
							Builds <TabsCount>{builds.total === undefined ? "—" : formatTotal(builds.total)}</TabsCount>
						</TabsTrigger>
					</TabsList>
				</div>
				<TabsContent value="deploys" className="pt-1.5">
					<div aria-busy={isLoadingMore}>
						<DataTable
							table={deployTable}
							inset
							label="Deployments"
							className="[--row-h:58px]"
							empty={<div className="flex h-24 items-center justify-center text-fg3">No deployments yet.</div>}
							onRowClick={(row) => {
								setOpen(open === row.id ? null : row.id);
							}}
							renderExpanded={({ id, original: { rows } }) =>
								open === id
									? rows.map((row) => (
											<section
												key={row.region.name}
												aria-label={rows.length > 1 ? `Deployment in ${row.region.name}` : undefined}
											>
												{rows.length > 1 && (
													<h3 className="m-0 pt-3 font-mono text-[12px] font-medium text-fg2 md:pl-9">
														{row.region.name}
													</h3>
												)}
												<Detail
													row={row}
													logsHref={logsHref}
													onRollback={onRollback}
													onViewSpec={onViewSpec}
													onOpenBuild={goBuild}
												/>
											</section>
										))
									: null
							}
						/>
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
						onNext={nextPage(deployRows, isLoadingMore, hasMore, onLoadMore, setPagination)}
					/>
					{historyError !== null && (
						<div role="alert" className="px-5 pb-3 text-bad-fg">
							{getErrorMessage(historyError, "Could not load older deployments. Try Next again.")}
						</div>
					)}
				</TabsContent>
				<TabsContent value="builds" className="pt-1.5">
					<div aria-busy={builds.isFetching}>
						<BuildsTable
							builds={builds.builds}
							actors={actors}
							workspaceId={workspaceId}
							nowMs={nowMs}
							canWrite={canWrite}
							openId={openBuild}
							onOpen={setOpenBuild}
							deploymentFor={buildDeployment}
							logsHref={buildLogsHref}
							onCanceled={builds.refresh}
							emptyText={imageOnly ? `No builds. ${resourceName} runs a public image.` : "No builds yet."}
						/>
					</div>
					<Pager
						page={buildPager.page}
						total={builds.builds.length}
						pageSizes={BUILD_PAGE_SIZES}
						label={
							<>
								{tokenPageLabel(buildPager.page, BUILD_PAGE_SIZE, builds.builds.length, builds.total)}
								{limits !== undefined && (
									<HistoryKept
										count={limits.buildHistory}
										tip={`Loco keeps the last ${formatCount(limits.buildHistory)} builds for each resource.`}
									/>
								)}
							</>
						}
						firstLabel="Newest builds"
						prevLabel="Newer builds"
						nextLabel="Older builds"
						onFirst={buildPager.onFirst}
						onPrev={buildPager.onPrev}
						onNext={buildPager.onNext}
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
						{selectedStatus !== undefined && <StatusBadge {...selectedStatus} className="self-start" />}
						{selected.data?.build !== undefined && (
							<BuildDetail
								build={selected.data.build}
								actors={actors}
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
