import { DeploymentListView } from "@gen/loco/deployment/v1/deployment_pb";
import type { Build } from "@gen/loco/build/v1/build_pb";
import { deploymentMessage } from "@/hooks/deployment-watch";
import { skipToken, useQuery, useTransport } from "@connectrpc/connect-query";
import { getBuild } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { keepPreviousData, useQuery as useTanstackQuery } from "@tanstack/react-query";
import { functionalUpdate } from "@tanstack/react-table";
import { CheckIcon, CopyIcon, FileCodeIcon, GitCompareIcon, Undo2Icon, XIcon } from "lucide-react";
import { useState } from "react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import {
	CellText,
	DataTable,
	DataTableColumnsMenu,
	useDataTable,
	type DataColumn,
} from "@/components/design/DataTable";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { Dialog, DialogBody, DialogContent } from "@/components/design/Dialog";
import { Spinner } from "@/components/design/Spinner";
import { Pager } from "@/components/design/Pager";
import { DeploymentStatusBadge, StatusBadge } from "@/components/design/StatusBadge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/design/Tabs";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { useCopy } from "@/hooks/useCopy";
import { useLayout } from "@/lib/layout";
import { useShownPage, useTokenPage } from "@/hooks/useTokenPage";
import { tookLabel } from "@/lib/activity-deploys";
import { deploymentActor, deploymentMethod, deploymentTrigger } from "@/lib/deployment-utils";
import { getErrorMessage } from "@/lib/error-handler";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { maybeTsMs, readyMs } from "@/lib/time";
import { formatShortId } from "@/lib/utils";

import { buildStatusStyle, deploymentBuildId } from "./builds";
import {
	BuildDetail,
	BuildsTable,
	parseVisibility,
	TIME_CELL,
	TimeAgo,
	type BuildActor,
	type Visibility,
} from "./BuildsTable";
import { canRollBackTo, DEFAULT_PAGE_SIZE, depTag, PAGE_SIZES, REFRESH_MS, tookMs, type RegionView } from "./model";
import { useResourceBuilds } from "./useBuilds";
import { InlineCode } from "@/components/design/InlineCode";

const BUILD_PAGE_SIZE = 5;
const BUILD_PAGE_SIZES = [BUILD_PAGE_SIZE];
const HISTS = ["deploys", "builds"] as const;
const histParser = parseAsStringLiteral(HISTS).withDefault("deploys").withOptions({ history: "replace" });
const buildPagesParser = pageTokensParser.withOptions({ history: "replace" });

interface Row {
	dep: Deployment;
	region: RegionView;
}

/** The regions of one deployment, one row per region. */
interface RolloutRow {
	key: string;
	rows: [Row, ...Row[]];
}

function historyMessage(dep: Deployment): string {
	return deploymentMessage(dep) || (dep.commit?.message ?? "");
}

const HISTORY_CARD = "mt-2.5 min-w-0 rounded-card bg-panel pt-1.5";

/** The ids of the columns the table shows when the viewer has not chosen. */
const HIDDEN_DEPLOY_COLUMNS = ["commit", "region", "trigger", "message", "deployed"];

/** Text with a copy button that shows while the row is hovered or the button has focus. */
function CopyCell({ text, copy, label }: { text: string; copy: string; label: string }) {
	const [copied, copyText] = useCopy();
	return (
		<span className="flex min-w-0 items-center gap-1">
			<CellText text={text} className="font-mono text-[12px] text-foreground" />
			<Button
				variant="ghost"
				size="icon-xs"
				aria-label={copied === null ? label : "Copied"}
				title={label}
				className="size-5 opacity-0 focus-visible:opacity-100 [tr:hover_&]:opacity-100"
				onClick={() => {
					copyText(copy, copy);
				}}
			>
				{copied === null ? <CopyIcon /> : <CheckIcon />}
			</Button>
		</span>
	);
}

function DeployedBy({ deployment }: { deployment: Deployment }) {
	const method = deploymentMethod(deployment);
	return (
		<span className="flex min-w-0 items-baseline gap-2">
			<CellText text={deploymentActor(deployment)} className="text-[13px] text-foreground" />
			{method !== "" && <span className="shrink-0 text-[12px] text-fg3">{method}</span>}
		</span>
	);
}

function RolloutActions({
	rollout,
	canWrite,
	onRollback,
	onCompareSpec,
}: {
	rollout: RolloutRow;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	onCompareSpec: (dep: Deployment, current: Deployment) => void;
}) {
	const dep = rollout.rows[0].dep;
	const current = rollout.rows.find((r) => r.region.current !== undefined && r.region.current.id !== dep.id)?.region
		.current;
	const rollbacks = canWrite ? rollout.rows.filter((r) => canRollBackTo(r.dep, r.region)) : [];
	if (rollbacks.length === 0 && current === undefined) return null;
	return (
		<RowMenu label={`Actions for ${formatShortId(dep.id)}`}>
			{rollbacks.map((r) => (
				<DropdownMenuItem
					key={r.region.name}
					onClick={() => {
						onRollback(r.dep, r.region.current);
					}}
				>
					<Undo2Icon className="text-fg3" />
					{rollout.rows.length > 1 ? `Roll back to this in ${r.region.name}` : "Roll back to this"}
				</DropdownMenuItem>
			))}
			{current !== undefined && (
				<DropdownMenuItem
					onClick={() => {
						onCompareSpec(dep, current);
					}}
				>
					<GitCompareIcon className="text-fg3" />
					Compare spec with current
				</DropdownMenuItem>
			)}
		</RowMenu>
	);
}

function rolloutColumns({
	nowMs,
	canWrite,
	onRollback,
	onViewSpec,
	onCompareSpec,
	onOpenBuild,
}: {
	nowMs: number;
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	onViewSpec: (dep: Deployment) => void;
	onCompareSpec: (dep: Deployment, current: Deployment) => void;
	onOpenBuild: (buildId: string) => void;
}): DataColumn<RolloutRow>[] {
	const lead = (rollout: RolloutRow) => rollout.rows[0].dep;
	return [
		{
			id: "id",
			header: "Deployment ID",
			size: 130,
			minSize: 100,
			meta: { fit: true, fitMax: 160, label: "Deployment ID" },
			enableHiding: false,
			cell: ({ row: { original: rollout } }) => (
				<CopyCell
					text={formatShortId(rollout.rows[0].dep.id)}
					copy={rollout.rows[0].dep.id}
					label="Copy deployment ID"
				/>
			),
		},
		{
			id: "status",
			header: "Status",
			size: 110,
			minSize: 90,
			meta: { fit: true, fitMax: 140 },
			enableHiding: false,
			cell: ({ row: { original: rollout } }) => <DeploymentStatusBadge phase={lead(rollout).status} />,
		},
		{
			id: "build",
			header: "Build",
			size: 110,
			minSize: 80,
			meta: { fit: true, fitMax: 140 },
			cell: ({ row: { original: rollout } }) => {
				const buildId = deploymentBuildId(lead(rollout));
				if (buildId === undefined) return <span className="text-fg3">—</span>;
				return (
					<Button
						variant="plain-link"
						aria-label={`Build ${formatShortId(buildId)}`}
						className="font-mono text-[12px] text-foreground hover:text-brand"
						onClick={() => {
							onOpenBuild(buildId);
						}}
					>
						{formatShortId(buildId)}
					</Button>
				);
			},
		},
		{
			id: "actor",
			header: "Deployed by",
			size: 200,
			minSize: 120,
			meta: { grow: true },
			cell: ({ row: { original: rollout } }) => <DeployedBy deployment={lead(rollout)} />,
		},
		{
			id: "commit",
			header: "Commit",
			size: 110,
			minSize: 90,
			meta: { fit: true, fitMax: 160, defaultHidden: true },
			cell: ({ row: { original: rollout } }) => {
				const dep = lead(rollout);
				return dep.commit === undefined ? (
					<CellText text={depTag(dep)} className="font-mono text-[12px] text-foreground" />
				) : (
					<CopyCell text={depTag(dep)} copy={dep.commit.sha} label="Copy SHA" />
				);
			},
		},
		{
			id: "region",
			header: "Region(s)",
			size: 120,
			minSize: 100,
			meta: { fit: true, fitMax: 160, defaultHidden: true },
			cell: ({ row: { original: rollout } }) => (
				<CellText text={rollout.rows.map((r) => r.region.name).join(", ")} className="font-mono text-[12px] text-fg2" />
			),
		},
		{
			id: "trigger",
			header: "Trigger",
			size: 100,
			minSize: 80,
			meta: { fit: true, fitMax: 140, defaultHidden: true },
			cell: ({ row: { original: rollout } }) => (
				<CellText text={deploymentTrigger(lead(rollout))} className="text-fg2" />
			),
		},
		{
			id: "message",
			header: "Message",
			size: 160,
			minSize: 72,
			meta: { defaultHidden: true },
			cell: ({ row: { original: rollout } }) => <CellText text={historyMessage(lead(rollout))} className="text-fg2" />,
		},
		{
			id: "took",
			header: "Duration",
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
			size: 100,
			minSize: 72,
			meta: { align: "end", cellClassName: TIME_CELL },
			cell: ({ row: { original: rollout } }) => <TimeAgo ms={maybeTsMs(rollout.rows[0].dep.createdAt)} nowMs={nowMs} />,
		},
		{
			id: "deployed",
			header: "Deployed",
			size: 100,
			minSize: 72,
			meta: { align: "end", cellClassName: TIME_CELL, defaultHidden: true },
			cell: ({ row: { original: rollout } }) => <TimeAgo ms={readyMs(rollout.rows[0].dep)} nowMs={nowMs} />,
		},
		{
			id: "spec",
			header: "Spec",
			size: 100,
			minSize: 80,
			meta: { fit: true, fitMax: 120 },
			cell: ({ row: { original: rollout } }) => (
				<Button
					variant="plain-link"
					aria-label={`View spec of ${formatShortId(lead(rollout).id)}`}
					className="text-[13px]"
					onClick={() => {
						onViewSpec(lead(rollout));
					}}
				>
					<FileCodeIcon className="hidden max-md:block" aria-hidden />
					<span className="max-md:hidden">View spec</span>
				</Button>
			),
		},
		{
			id: "actions",
			header: ({ table }) => <DataTableColumnsMenu table={table} />,
			meta: { label: "Actions", pinEnd: true },
			enableHiding: false,
			cell: ({ row: { original: rollout } }) => (
				<RolloutActions rollout={rollout} canWrite={canWrite} onRollback={onRollback} onCompareSpec={onCompareSpec} />
			),
		},
	];
}

export function DeploymentsSection({
	resourceId,
	resourceName,
	workspaceId,
	environmentId,
	regions,
	buildLogsHref,
	canWrite,
	canReadWorkspace,
	onRollback,
	onViewSpec,
	onCompareSpec,
	nowMs,
}: {
	resourceId: string;
	resourceName: string;
	workspaceId: string;
	environmentId: string | undefined;
	regions: RegionView[];
	buildLogsHref: (buildId: string, build?: Build) => string | undefined;
	canWrite: boolean;
	canReadWorkspace: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
	/** Opens dep's spec, offering the other deployments on the page to compare with. */
	onViewSpec: (dep: Deployment, shown: Deployment[]) => void;
	/** Opens dep's spec compared with the spec of `current`. */
	onCompareSpec: (dep: Deployment, current: Deployment) => void;
	nowMs: number;
}) {
	const transport = useTransport();
	const [deploySize, setDeploySize] = useState(DEFAULT_PAGE_SIZE);
	const [deployPages, setDeployPages] = useState<string[]>([]);
	const [buildFilter, setBuildFilter] = useState<string | null>(null);
	const deploymentFilters = {
		resourceId,
		environmentId: environmentId ?? "",
		...(buildFilter === null ? {} : { buildId: buildFilter }),
	};
	const history = useTokenPage(
		listDeployments,
		{ ...deploymentFilters, pageSize: deploySize, view: DeploymentListView.FULL },
		deployPages,
		{
			enabled: environmentId !== undefined,
			refetchInterval: REFRESH_MS,
			staleTime: REFRESH_MS,
			placeholderData: keepPreviousData,
		},
		deploymentFilters,
	);
	const deployPager = tokenPager(deployPages, history.nextPageToken, (pages) => {
		if (!history.query.isPlaceholderData) setDeployPages(pages ?? []);
	});
	const deployments = history.query.data?.deployments ?? [];
	const deployShown = useShownPage(
		deployPager.page,
		tokenPageLabel(deployPager.page, deploySize, deployments.length, history.total),
		history.query.isPlaceholderData,
	);
	const [openBuild, setOpenBuild] = useState<string | null>(null);
	const [buildPages, setBuildPages] = useQueryState("builds", buildPagesParser);
	const builds = useResourceBuilds(resourceId, environmentId, BUILD_PAGE_SIZE, buildPages);
	const buildPager = tokenPager(buildPages, builds.nextPageToken, (pages) => {
		void setBuildPages(pages);
	});
	const [selectedBuild, setSelectedBuild] = useState<string | null>(null);
	const selected = useQuery(getBuild, selectedBuild === null ? skipToken : { buildId: selectedBuild });
	const selectedStatus = selected.data?.build === undefined ? undefined : buildStatusStyle(selected.data.build.status);
	const { data: membersRes } = useTanstackQuery({
		...workspaceMembersOptions(transport, workspaceId),
		enabled: canReadWorkspace,
	});
	const [hist, setHist] = useQueryState("hist", histParser);

	const regionIndex = new Map(regions.map((r, i) => [r.name, i]));
	const rollouts: RolloutRow[] = deployments.flatMap((dep) => {
		const [first, ...rest] = [...new Set(dep.placements.map((p) => p.region).filter((r) => r !== ""))]
			.toSorted((a, b) => (regionIndex.get(a) ?? regions.length) - (regionIndex.get(b) ?? regions.length))
			.map((name) => ({
				dep,
				region: regions[regionIndex.get(name) ?? -1] ?? {
					name,
					status: undefined,
					history: [],
					current: undefined,
					serving: undefined,
					lastGood: undefined,
				},
			}));
		return first === undefined ? [] : [{ key: dep.id, rows: [first, ...rest] }];
	});
	const goBuild = (buildId: string) => {
		if (builds.builds.some((build) => build.id === buildId)) {
			void setHist("builds");
			setOpenBuild(buildId);
			return;
		}
		setSelectedBuild(buildId);
	};
	const [shownColumns, setShownColumns] = useLayout("tables.history.deployments", parseVisibility);
	const columnVisibility: Visibility = {
		...Object.fromEntries(HIDDEN_DEPLOY_COLUMNS.map((id) => [id, false])),
		...shownColumns,
	};
	const deployTable = useDataTable("deployments", {
		columns: rolloutColumns({
			nowMs,
			canWrite,
			onRollback,
			onViewSpec: (dep) => {
				onViewSpec(dep, deployments);
			},
			onCompareSpec,
			onOpenBuild: goBuild,
		}),
		data: rollouts,
		getRowId: (rollout) => rollout.key,
		state: { columnVisibility },
		onColumnVisibilityChange: (updater) => {
			setShownColumns(functionalUpdate(updater, columnVisibility));
		},
	});

	const actors = new Map<string, BuildActor>(
		(membersRes?.members ?? []).map((m) => [
			m.userId,
			{ name: m.userName, email: m.userEmail, avatarUrl: m.userAvatarUrl },
		]),
	);
	const running = regions.flatMap((r) => (r.current === undefined ? [] : [r.current]));
	const imageOnly = running.length > 0 && running.every((dep) => deploymentBuildId(dep) === undefined);

	// The build a detail is open for links to the newest deployment built from it.
	const linkedBuild = selectedBuild ?? openBuild;
	const built = useQuery(
		listDeployments,
		linkedBuild === null || environmentId === undefined
			? skipToken
			: { resourceId, environmentId, buildId: linkedBuild, pageSize: 1 },
	).data?.deployments[0];
	const showBuildDeployments = (buildId: string) => {
		void setHist("deploys");
		setBuildFilter(buildId);
		setDeployPages([]);
	};
	const buildDeployment = (buildId: string) => {
		if (built === undefined || deploymentBuildId(built) !== buildId) return undefined;
		return {
			tag: depTag(built),
			onOpen: () => {
				setSelectedBuild(null);
				showBuildDeployments(buildId);
			},
		};
	};

	return (
		<section aria-label="History" className="flex min-w-0 flex-col gap-2.5">
			<Tabs
				value={hist}
				onValueChange={(next: string) => {
					void setHist(HISTS.find((h) => h === next) ?? "deploys");
				}}
			>
				<div className="flex items-center gap-2">
					<TabsList variant="segmented" aria-label="History">
						<TabsTrigger value="deploys">Deployments</TabsTrigger>
						<TabsTrigger value="builds">Builds</TabsTrigger>
					</TabsList>
					{buildFilter !== null && hist === "deploys" && (
						<Button
							variant="outline"
							size="sm"
							aria-label="Clear build filter"
							onClick={() => {
								setBuildFilter(null);
								setDeployPages([]);
							}}
						>
							Build <span className="font-mono">{formatShortId(buildFilter)}</span>
							<XIcon className="text-fg3" />
						</Button>
					)}
				</div>
				<TabsContent value="deploys" className={HISTORY_CARD}>
					<div aria-busy={history.query.isPlaceholderData}>
						<DataTable
							table={deployTable}
							band
							label="Deployments"
							loading={history.query.isLoading}
							empty={
								history.query.error === null ? (
									<div className="flex h-24 items-center justify-center text-fg3">No deployments yet.</div>
								) : null
							}
						/>
					</div>
					<Pager
						total={deployments.length}
						pageSizes={PAGE_SIZES}
						pageSize={deploySize}
						onPageSize={(size) => {
							setDeploySize(size);
							setDeployPages([]);
						}}
						{...deployShown}
						onPrev={deployPager.onPrev}
						onNext={deployPager.onNext}
					/>
					{history.query.error !== null && (
						<div role="alert" className="px-5 pb-3 text-bad-fg">
							{getErrorMessage(history.query.error, "Could not load deployments.")}
						</div>
					)}
				</TabsContent>
				<TabsContent value="builds" className={HISTORY_CARD}>
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
							onShowDeployments={showBuildDeployments}
							logsHref={buildLogsHref}
							onCanceled={builds.refresh}
							emptyText={
								imageOnly ? (
									<>
										No builds. <InlineCode>{resourceName}</InlineCode> runs a public image.
									</>
								) : (
									"No builds yet."
								)
							}
						/>
					</div>
					<Pager
						page={buildPager.page}
						total={builds.builds.length}
						pageSizes={BUILD_PAGE_SIZES}
						label={tokenPageLabel(buildPager.page, BUILD_PAGE_SIZE, builds.builds.length, builds.total)}
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
								logsHref={buildLogsHref(selected.data.build.id, selected.data.build)}
								onCanceled={() => {
									builds.refresh();
									void selected.refetch();
								}}
							/>
						)}
					</DialogBody>
				</DialogContent>
			</Dialog>
		</section>
	);
}
