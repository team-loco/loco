import { deploymentMessage } from "@/hooks/deployment-watch";
import { useQuery as useConnectQuery, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { HOUR_MS } from "@/lib/obs-time";
import {
	TRAFFIC_METRICS,
	latestTraffic,
	trafficPoints,
	trafficSeries,
	trafficValue,
	type TrafficPoint,
} from "@/lib/traffic";
import { functionalUpdate } from "@tanstack/react-table";
import { ArrowRightIcon, FilePenLineIcon, GlobeIcon } from "lucide-react";
import { createContext, use } from "react";
import { parseAsString, useQueryState, useQueryStates } from "nuqs";
import { Link, useLocation, useNavigate } from "react-router";
import type { DeploymentBucket } from "@gen/loco/deployment/v1/deployment_pb";
import { queryDeploymentBuckets } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { ResourceOrder, ResourceStatus, ResourceType, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { ErrorState } from "@/components/design/ErrorState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { Pager } from "@/components/design/Pager";
import { RowMenu } from "@/components/design/RowMenu";
import { SearchInput } from "@/components/design/SearchInput";
import { DeployMarkers } from "@/components/design/DeployMarkers";
import { Sparkline } from "@/components/design/Sparkline";
import { ResourceStatusBadge, RolloutBadge, StatusBadge } from "@/components/design/StatusBadge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useCurrentDeployments } from "@/hooks/useWorkspaceDeployments";
import { deployMarkers, MARKER_NAMES } from "@/lib/deployMarkers";
import { deploymentImage } from "@/lib/deployment-utils";
import { DRAFT_NOTE } from "@/lib/drafts";
import { imageVersion } from "@/lib/image";
import { rolloutState } from "@/lib/resource-status";
import { draftSearch, RESOURCE_VIEWS, resourcesPath, resourceViewParser } from "@/lib/routes";
import { agoLabel, msToTimestamp, readyMs } from "@/lib/time";
import { pageTokensParser, tokenPageLabel } from "@/lib/paging";
import { sortParser } from "@/lib/sort-parser";
import { cn } from "@/lib/utils";
import {
	shownDrafts,
	statusCounts,
	statusInput,
	useMatchingResourceIds,
	useResourcePage,
} from "@/pages/resources/query";
import { StatusFilter } from "@/pages/resources/ResourceFilters";
import { DRAFT, sortableIds } from "@/pages/resources/table";

import { ArchitectureDiagram } from "./ArchitectureDiagram";
import { DashboardRowMenu } from "./DashboardRowMenu";
import {
	ALL,
	isDraftItem,
	itemKey,
	itemName,
	TYPE_FILTERS,
	viewAllSearch,
	type DashboardItem,
	type DraftItem,
	type RowFilter,
} from "./resourceRows";
import { envResource, useDashboardData, type EnvResource } from "./useDashboardData";

const PAGE_SIZE = 25;
const MAX_PIPS = 12;
const ROW_BUCKETS = 6;
const MAP_SKELETON_PX = 320;

function replicaTotal(item: DashboardItem): number {
	return isDraftItem(item) ? 0 : item.totalReplicas;
}

function lastDeployMs(item: DashboardItem): number {
	return isDraftItem(item) || item.last === undefined ? 0 : (readyMs(item.last) ?? 0);
}

function regionNote(item: EnvResource): string {
	return item.environment?.regions.find((r) => (r.message ?? "") !== "")?.message ?? "";
}

function statusNote(item: EnvResource): string {
	if (item.neverDeployed) return "Not deployed";
	switch (item.status) {
		case ResourceStatus.DEPLOYING:
			return item.last === undefined ? "" : deploymentMessage(item.last);
		case ResourceStatus.DEGRADED:
			return regionNote(item);
		case ResourceStatus.UNAVAILABLE:
			return regionNote(item);
		case ResourceStatus.UNKNOWN:
			return regionNote(item);
		case ResourceStatus.HEALTHY:
			return regionNote(item);
		case ResourceStatus.UNSPECIFIED:
			return "";
	}
}

function ReplicaPips({ desired, ready, degraded }: { desired: number; ready: number; degraded: boolean }) {
	return (
		<span className="flex shrink-0 gap-0.5" aria-hidden="true">
			{Array.from({ length: desired }, (_, i) => (
				<span
					key={i}
					className={cn(
						"h-4 w-1.5 shrink-0 rounded-[1.5px]",
						i >= ready ? "ring-1 ring-line2 ring-inset" : degraded ? "bg-warn" : "bg-foreground",
					)}
				/>
			))}
		</span>
	);
}

const NAME_CLASS = "text-left text-[14.5px] font-medium text-foreground";

function Regions({ item }: { item: DashboardItem }) {
	return (
		<div className="flex flex-col font-mono text-[12px] leading-[1.6] text-fg2">
			{item.regions.map((g) => (
				<span key={g.region}>{g.region}</span>
			))}
		</div>
	);
}

interface DashboardCellValues {
	traffic: TrafficPoint[];
	/** The last hour's deployment buckets of each row's resource. */
	deploys: ReadonlyMap<string, DeploymentBucket[]>;
	trafficLoading: boolean;
	trafficError: boolean;
	orgId: string;
	workspaceId: string;
	envName: string;
	nowMs: number;
	hrefFor: (item: DashboardItem) => string;
}

const DashboardCellContext = createContext<DashboardCellValues | null>(null);

function useCells(): DashboardCellValues {
	const ctx = use(DashboardCellContext);
	if (ctx === null) throw new Error("DashboardCellContext provider missing");
	return ctx;
}

function StatusCell({ item }: { item: DashboardItem }) {
	if (isDraftItem(item)) {
		return <StatusBadge glyph="draft" label="Draft" />;
	}
	const note = statusNote(item);
	return (
		<div className="flex min-w-0 flex-col items-start gap-[3px]">
			<ResourceStatusBadge status={item.status} />
			<RolloutBadge rollout={rolloutState(item.environment?.regions ?? [])} compact />
			{note !== "" && <CellText text={note} className="max-w-full text-[12px] text-fg3" />}
		</div>
	);
}

function NameCell({ item }: { item: DashboardItem }) {
	const { hrefFor } = useCells();
	const draft = isDraftItem(item);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<CellText text={itemName(item)} render={<Link to={hrefFor(item)} />} className={NAME_CLASS} />
			<CellText text={draft ? DRAFT_NOTE : (item.resource.description ?? "")} className="text-[12.5px] text-fg3" />
		</div>
	);
}

function ReplicasCell({ item }: { item: DashboardItem }) {
	if (isDraftItem(item)) return <span className="font-mono text-[12px] text-fg4">—</span>;
	const degraded = item.status === ResourceStatus.DEGRADED;
	const meter = item.totalReplicas > MAX_PIPS;
	const ready = item.regions.reduce((n, g) => n + g.ready, 0);
	const missing = Math.max(0, item.totalReplicas - ready);
	const summary = `${ready.toString()} of ${item.totalReplicas.toString()} ready${missing ? `, ${missing.toString()} starting` : ""}`;
	const label = [
		summary,
		...item.regions.map((g) => `${g.region}: ${g.ready.toString()}/${g.desired.toString()}`),
	].join("\n");
	const replicas = (
		<div className="flex flex-col">
			{item.regions.map((g) => (
				<span key={g.region} className="flex h-[19px] items-center gap-2.5">
					{meter ? (
						<span className="relative block h-1.5 w-[44px] overflow-hidden rounded-[3px] bg-line" aria-hidden="true">
							<span
								className={cn(
									"absolute inset-y-0 left-0 rounded-[3px] opacity-85",
									degraded ? "bg-warn" : "bg-foreground",
								)}
								style={{
									width: `${(g.desired === 0 ? 0 : Math.min(1, g.ready / g.desired) * 100).toString()}%`,
								}}
							/>
						</span>
					) : (
						<ReplicaPips desired={g.desired} ready={g.ready} degraded={degraded} />
					)}
					<span className="font-mono text-[12px] text-fg2 tabular-nums">
						{g.ready.toString()}/{g.desired.toString()}
					</span>
				</span>
			))}
		</div>
	);
	if (!meter) return replicas;
	return (
		<Tooltip>
			<TooltipTrigger render={replicas} tabIndex={0} aria-label={label} />
			<TooltipContent className="whitespace-pre-line">{label}</TooltipContent>
		</Tooltip>
	);
}

function LastDeployCell({ item }: { item: DashboardItem }) {
	if (isDraftItem(item)) {
		return <CellText text={item.draft.image} className="font-mono text-[12px] text-fg3" />;
	}
	const last = item.last;
	if (last === undefined) return <span className="text-fg4">—</span>;
	const message = deploymentMessage(last);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<CellText text={imageVersion(deploymentImage(last))} className="font-mono text-[12px]" />
			<CellText text={message !== "" ? message : "Running"} className="text-[12.5px] text-fg3" />
		</div>
	);
}

function DeployedCell({ item }: { item: DashboardItem }) {
	const { nowMs } = useCells();
	const last = isDraftItem(item) ? undefined : item.last;
	const ready = last === undefined ? undefined : readyMs(last);
	return <>{ready === undefined ? "—" : agoLabel(ready, nowMs)}</>;
}

function MenuCell({ item }: { item: DashboardItem }) {
	const { orgId, workspaceId, envName, hrefFor } = useCells();
	if (isDraftItem(item))
		return (
			<RowMenu label={`Actions for ${item.draft.name}`} title={item.draft.name}>
				<DropdownMenuItem render={<Link to={hrefFor(item)} />}>
					<FilePenLineIcon className="text-fg3" />
					Open draft
				</DropdownMenuItem>
			</RowMenu>
		);
	return <DashboardRowMenu item={item} orgId={orgId} workspaceId={workspaceId} envName={envName} />;
}

function TrafficCell({ item }: { item: DashboardItem }) {
	const { traffic, deploys, trafficLoading, trafficError, nowMs } = useCells();
	if (isDraftItem(item)) return <span className="text-fg3">—</span>;
	const markers = deployMarkers(deploys.get(item.resource.id) ?? [], nowMs - HOUR_MS, nowMs, nowMs);
	const rate = trafficLoading
		? "…"
		: trafficError
			? "Unavailable"
			: trafficValue(latestTraffic(traffic, item.resource.id)?.requests, "/s");
	return (
		<span
			className="flex items-center gap-2.5"
			title={trafficError ? "Failed to load traffic" : "Requests per second over the last hour"}
		>
			<span className="relative min-w-0 flex-1">
				<Sparkline
					values={trafficLoading || trafficError ? [] : trafficSeries(traffic, "requests", item.resource.id)}
					className="h-6 text-fg2"
				/>
				{!trafficLoading && !trafficError && <DeployMarkers markers={markers} />}
			</span>
			<span className="min-w-[62px] shrink-0 text-right font-mono text-[12px] whitespace-nowrap">{rate}</span>
		</span>
	);
}

export const COLUMNS: DataColumn<DashboardItem>[] = [
	{
		id: "status",
		header: "Status",
		accessorFn: (item) => (isDraftItem(item) ? ResourceStatus.UNSPECIFIED : item.status),
		size: 130,
		minSize: 90,
		maxSize: 160,
		meta: { fit: true },
		cell: ({ row }) => <StatusCell item={row.original} />,
	},
	{
		id: "name",
		header: "Resource",
		accessorFn: itemName,
		size: 160,
		meta: { grow: true, primary: true },
		cell: ({ row }) => <NameCell item={row.original} />,
	},
	{
		id: "regions",
		header: "Region",
		accessorFn: (item) => item.regions[0]?.region ?? "",
		size: 100,
		minSize: 80,
		maxSize: 160,
		meta: { fit: true },
		cell: ({ row }) => <Regions item={row.original} />,
	},
	{
		id: "replicas",
		header: "Replicas",
		accessorFn: replicaTotal,
		sortDescFirst: true,
		size: 184,
		minSize: 80,
		maxSize: 184,
		meta: { fit: true },
		cell: ({ row }) => <ReplicasCell item={row.original} />,
	},
	{
		id: "traffic",
		header: "Traffic, last hour",
		size: 116,
		minSize: 110,
		cell: ({ row }) => <TrafficCell item={row.original} />,
	},
	{
		id: "last",
		header: "Last deploy",
		size: 140,
		minSize: 100,
		maxSize: 200,
		meta: { fit: true },
		cell: ({ row }) => <LastDeployCell item={row.original} />,
	},
	{
		id: "deploy",
		header: "Deployed",
		accessorFn: lastDeployMs,
		sortDescFirst: true,
		size: 96,
		minSize: 72,
		maxSize: 120,
		meta: {
			fit: true,
			align: "end",
			cellClassName: "font-mono text-[12px] whitespace-nowrap text-fg3",
		},
		cell: ({ row }) => <DeployedCell item={row.original} />,
	},
	{
		id: "menu",
		meta: { pinEnd: true },
		cell: ({ row }) => <MenuCell item={row.original} />,
	},
];

const TABLE_PARAMS = {
	status: parseAsString.withDefault(ALL),
	type: parseAsString.withDefault(ALL),
	region: parseAsString.withDefault(ALL),
	query: parseAsString.withDefault(""),
	sort: sortParser(sortableIds(COLUMNS), { id: "status", desc: false }),
	page: pageTokensParser,
};
const TABLE_URL_KEYS = { query: "q" };

const ORDERS: Record<string, { orderBy: ResourceOrder }> = {
	status: { orderBy: ResourceOrder.STATUS },
	name: { orderBy: ResourceOrder.NAME },
	regions: { orderBy: ResourceOrder.REGION },
	replicas: { orderBy: ResourceOrder.REPLICAS },
	deploy: { orderBy: ResourceOrder.DEPLOYED },
};

/** The map of every resource the filters match; it reads the environment's every deployment while shown. */
function DashboardMap({
	orgId,
	workspaceId,
	environmentId,
	regions,
	drafts,
	matching,
	empty,
	hrefFor,
	onShowTable,
}: {
	orgId: string;
	workspaceId: string;
	environmentId: string;
	regions: RegionInfo[];
	drafts: DraftItem[];
	/** The resources the filters match; undefined matches all of them. */
	matching: ReadonlySet<string> | undefined;
	empty: React.ReactNode;
	hrefFor: (item: DashboardItem) => string;
	onShowTable: () => void;
}) {
	const data = useDashboardData(orgId, workspaceId, environmentId);
	if (data.error !== null)
		return <ErrorState error={data.error} what="resources" rpc={listWorkspaceResources} onRetry={data.refetch} />;
	if (data.isLoading) return <Skeleton className="m-4" style={{ height: MAP_SKELETON_PX }} />;
	const rows: DashboardItem[] = [...data.envResources.filter((r) => matching?.has(r.resource.id) !== false), ...drafts];
	if (rows.length === 0) return <div className="border-t border-line2">{empty}</div>;
	return (
		<ArchitectureDiagram
			rows={rows}
			regionOrder={regions.map((r) => r.region)}
			environmentId={environmentId}
			hrefFor={hrefFor}
			onShowTable={onShowTable}
		/>
	);
}

export function ResourcesTable({
	total,
	drafts,
	regions,
	orgId,
	workspaceId,
	environmentId,
	envName,
	nowMs,
	hrefFor,
}: {
	/** The resources of the environment. */
	total: number;
	drafts: DraftItem[];
	regions: RegionInfo[];
	orgId: string;
	workspaceId: string;
	environmentId: string;
	envName: string;
	nowMs: number;
	hrefFor: (id: string) => string;
}) {
	const transport = useTransport();
	const traffic = useQuery(
		metricsQueryOptions(transport, {
			workspaceId,
			environmentId,
			resourceIds: [],
			...TRAFFIC_METRICS[0],
			spanMs: HOUR_MS,
		}),
	);
	const trafficData = trafficPoints([traffic.data ?? []]);
	const { search: locationSearch } = useLocation();
	const navigate = useNavigate();
	const [view, setView] = useQueryState("view", resourceViewParser.withOptions({ history: "replace" }));
	const isMap = view === "map";
	const [state, setState] = useQueryStates(TABLE_PARAMS, {
		history: "replace",
		urlKeys: TABLE_URL_KEYS,
	});
	const filter: RowFilter = {
		status: state.status,
		type: state.type,
		region: state.region,
		query: state.query,
	};
	const hasFilters =
		filter.status !== ALL || filter.type !== ALL || filter.region !== ALL || filter.query.trim() !== "";
	const draftsOnly = filter.status === DRAFT;
	const sorting = [state.sort];

	const matchedBy = {
		workspaceId,
		environmentId,
		environmentScoped: true,
		query: filter.query.trim(),
		...statusInput(filter.status),
		type: TYPE_FILTERS.find((o) => o.value === filter.type)?.type ?? ResourceType.UNSPECIFIED,
		region: filter.region === ALL ? "" : filter.region,
	};
	// Region counts ignore the region and status filters, and so does All regions.
	const regionsCounted = filter.region === ALL && filter.status === ALL;
	const { listed, pager, allRegions } = useResourcePage(
		{ ...matchedBy, ...ORDERS[state.sort.id], descending: state.sort.desc, pageSize: PAGE_SIZE },
		{ ...matchedBy, includeFacets: true },
		regionsCounted ? undefined : { ...matchedBy, ...statusInput(ALL), region: "" },
		state.page,
		(page) => void setState({ page }),
		draftsOnly,
	);
	const matching = useMatchingResourceIds(matchedBy, isMap && hasFilters && !draftsOnly).data;

	const draftRegions = (d: DraftItem) => d.regions.map((g) => g.region);
	const shownItems = shownDrafts(drafts, filter, draftRegions);
	const draftCount = shownDrafts(drafts, { ...filter, status: DRAFT }, draftRegions).length;
	const pageResources = listed.query.data?.resources ?? [];
	const current = useCurrentDeployments(pageResources, environmentId);
	const serverRows = draftsOnly ? [] : pageResources.map((r, i) => envResource(r, current[i] ?? [], environmentId));
	const data: DashboardItem[] = [...serverRows, ...(state.page.length === 0 ? shownItems : [])];
	const pageIds = serverRows.map((r) => r.resource.id);
	const deployBuckets = useConnectQuery(
		queryDeploymentBuckets,
		{
			workspaceId,
			environmentId,
			resourceIds: pageIds,
			groupByResource: true,
			since: msToTimestamp(nowMs - HOUR_MS),
			until: msToTimestamp(nowMs),
			bucketCount: ROW_BUCKETS,
			maxResourceNames: MARKER_NAMES,
		},
		{ enabled: !isMap && pageIds.length > 0, placeholderData: keepPreviousData },
	).data?.buckets;
	const deploys = Map.groupBy(deployBuckets ?? [], (b) => b.resourceId);
	const matched = draftsOnly ? draftCount : listed.total;

	const table = useDataTable("dashboard-resources", {
		data,
		columns: COLUMNS,
		getRowId: itemKey,
		state: { sorting },
		manualSorting: true,
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) void setState({ sort: next, page: null });
		},
	});

	// In one environment a resource has one status and one type, so the facets add up to the All counts.
	const facets = listed.counts?.facets;
	const sum = (counts: readonly { count: number }[]) => counts.reduce((n, c) => n + c.count, 0);
	const statusAll = facets === undefined ? undefined : sum(facets.statuses) + facets.notDeployed;
	const counts = statusCounts(statusAll, facets, draftCount);
	const regionNames = new Set(regions.map((r) => r.region));
	for (const item of drafts) for (const g of item.regions) regionNames.add(g.region);
	for (const r of facets?.regions ?? []) regionNames.add(r.region);
	if (filter.region !== ALL) regionNames.add(filter.region);
	const typeOptions = [
		{ value: ALL, label: "All types", ...(facets === undefined ? {} : { count: sum(facets.types) }) },
		...TYPE_FILTERS.map((o) => ({
			value: o.value,
			label: o.label,
			count: facets?.types.find((t) => t.type === o.type)?.count ?? 0,
		})),
	];
	const regionOptions = [
		{ value: ALL, label: "All regions", ...(allRegions === undefined ? {} : { count: allRegions }) },
		...[...regionNames].map((name) => ({
			value: name,
			label: name,
			count: facets?.regions.find((r) => r.region === name)?.count ?? 0,
		})),
	];

	const update = (patch: Partial<RowFilter>) => {
		void setState({ ...patch, page: null });
	};
	const clearFilters = () => {
		update({ status: ALL, type: ALL, region: ALL, query: "" });
	};

	const actions = (
		<>
			<ToggleGroup
				variant="segmented"
				aria-label="View"
				value={[view]}
				onValueChange={(v: string[]) => {
					const next = RESOURCE_VIEWS.find((r) => r === v[0]);
					if (next !== undefined) void setView(next);
				}}
			>
				<ToggleGroupItem value="table">Table</ToggleGroupItem>
				<ToggleGroupItem value="map">Map</ToggleGroupItem>
			</ToggleGroup>
			<Button
				variant="outline"
				size="sm"
				nativeButton={false}
				render={<Link to={`${resourcesPath(orgId, workspaceId)}${viewAllSearch(envName, filter)}`} />}
			>
				View all
				<ArrowRightIcon />
			</Button>
		</>
	);

	const filters = (
		<>
			<StatusFilter
				label="Filter resources by status"
				counts={counts}
				status={filter.status}
				onStatus={(status) => {
					update({ status });
				}}
			/>
			<FilterMenu
				label="Type"
				value={filter.type}
				options={typeOptions}
				onChange={(type) => {
					update({ type });
				}}
			/>
			<FilterMenu
				label="Region"
				icon={<GlobeIcon />}
				mono
				value={filter.region}
				options={regionOptions}
				onChange={(region) => {
					update({ region });
				}}
			/>
			{hasFilters && (
				<Button variant="plain-link" className="px-1" onClick={clearFilters}>
					Clear filters
				</Button>
			)}
		</>
	);
	const search = (
		<SearchInput
			value={filter.query}
			placeholder="Search resources"
			aria-label="Search resources"
			onChange={(e) => {
				update({ query: e.target.value });
			}}
		/>
	);

	const query = filter.query.trim();
	const empty = (
		<p className="m-0 py-7 text-center text-[13px] text-fg3">
			{query === "" ? "No resources match these filters." : `Nothing matches “${query}”.`}{" "}
			<Button variant="plain-link" onClick={clearFilters}>
				Clear filters
			</Button>
		</p>
	);

	const itemHref = (item: DashboardItem) =>
		isDraftItem(item) ? draftSearch(locationSearch, item.draft.id) : hrefFor(item.resource.id);

	return (
		<Section title="Resources" count={matched} actions={actions} filters={filters} search={search}>
			{listed.error !== null ? (
				<ErrorState error={listed.error} what="resources" rpc={listWorkspaceResources} onRetry={listed.query.refetch} />
			) : isMap ? (
				<DashboardMap
					orgId={orgId}
					workspaceId={workspaceId}
					environmentId={environmentId}
					regions={regions}
					drafts={shownItems}
					matching={draftsOnly ? new Set() : hasFilters ? matching : undefined}
					empty={empty}
					hrefFor={itemHref}
					onShowTable={() => {
						void setView("table");
					}}
				/>
			) : (
				<>
					<DashboardCellContext
						value={{
							orgId,
							workspaceId,
							envName,
							nowMs,
							hrefFor: itemHref,
							traffic: trafficData,
							deploys,
							trafficLoading: traffic.isLoading,
							trafficError: traffic.error !== null,
						}}
					>
						<DataTable
							table={table}
							inset
							label="Resources"
							loading={listed.query.isPending || listed.resetting}
							onRowClick={(row) => void navigate(itemHref(row.original))}
							rowAction="navigate"
							className="[--row-h:62px]"
							empty={empty}
						/>
					</DashboardCellContext>
					<Pager
						{...pager}
						label={
							data.length === 0
								? `0 of ${(total + drafts.length).toString()}`
								: tokenPageLabel(pager.page, PAGE_SIZE, draftsOnly ? data.length : serverRows.length, matched)
						}
						total={data.length}
						pageSizes={[PAGE_SIZE]}
					/>
				</>
			)}
		</Section>
	);
}
