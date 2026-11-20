import { deploymentMessage } from "@/hooks/deployment-watch";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
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
import { ArrowRightIcon, GlobeIcon, SearchXIcon, XIcon } from "lucide-react";
import { createContext, use } from "react";
import { parseAsString, useQueryState, useQueryStates } from "nuqs";
import { Link, useLocation, useNavigate } from "react-router";
import { ResourceStatus, type RegionInfo, type ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import {
	CellText,
	DataTable,
	DataTablePager,
	pageWithin,
	useDataTable,
	type DataColumn,
} from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { DeployMarkers } from "@/components/design/DeployMarkers";
import { Sparkline } from "@/components/design/Sparkline";
import { ResourceStatusBadge, StatusBadge } from "@/components/design/StatusBadge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { deployMarkers } from "@/lib/deployMarkers";
import { deploymentImage } from "@/lib/deployment-utils";
import { DRAFT_NOTE } from "@/lib/drafts";
import { imageVersion } from "@/lib/image";
import { draftSearch, RESOURCE_VIEWS, resourcesPath, resourceViewParser } from "@/lib/routes";
import { readyMs, shortDurationLabel } from "@/lib/time";
import { pageParser } from "@/lib/paging";
import { sortParser } from "@/lib/sort-parser";
import { cn } from "@/lib/utils";
import { sortableIds, visibleStatusFilters } from "@/pages/resources/table";

import { ArchitectureDiagram } from "./ArchitectureDiagram";
import { DashboardRowMenu } from "./DashboardRowMenu";
import {
	ALL,
	filterRows,
	isDraftItem,
	itemKey,
	itemName,
	itemRank,
	itemType,
	itemStatusFilter,
	TYPE_FILTERS,
	viewAllSearch,
	type DashboardItem,
	type RowFilter,
} from "./resourceRows";
import type { EnvResource } from "./useDashboardData";

const PAGE_SIZE = 25;
const MAX_PIPS = 12;
const ROW_BUCKETS = 6;

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
			{note !== "" && <CellText text={note} className="max-w-full text-[12px] text-fg3" />}
		</div>
	);
}

function NameCell({ item }: { item: DashboardItem }) {
	const { hrefFor } = useCells();
	const draft = isDraftItem(item);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<CellText
				text={itemName(item)}
				render={<Link to={hrefFor(item)} />}
				className={cn(
					NAME_CLASS,
					"underline-offset-3 group-hover/row:underline",
					draft && "underline decoration-fg4 decoration-dashed",
				)}
			/>
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
	const summary = `${ready.toString()} of ${item.totalReplicas.toString()} ready${missing ? ` · ${missing.toString()} starting` : ""}`;
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
	return <>{ready === undefined ? "—" : shortDurationLabel(nowMs - ready)}</>;
}

function MenuCell({ item }: { item: DashboardItem }) {
	const { orgId, workspaceId, envName } = useCells();
	if (isDraftItem(item)) return null;
	return <DashboardRowMenu item={item} orgId={orgId} workspaceId={workspaceId} envName={envName} />;
}

function TrafficCell({ item }: { item: DashboardItem }) {
	const { traffic, trafficLoading, trafficError, nowMs } = useCells();
	if (isDraftItem(item)) return <span className="text-fg3">—</span>;
	const markers = deployMarkers(
		item.envDeployments.map((deployment) => ({ deployment, resource: item.resource.name })),
		nowMs - HOUR_MS,
		nowMs,
		ROW_BUCKETS,
		nowMs,
	);
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
		accessorFn: itemRank,
		sortFn: "basic",
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
		sortFn: (a, b) => itemName(a.original).localeCompare(itemName(b.original)),
		size: 160,
		meta: { grow: true, primary: true },
		cell: ({ row }) => <NameCell item={row.original} />,
	},
	{
		id: "regions",
		header: "Region",
		accessorFn: (item) => item.regions[0]?.region ?? "",
		sortFn: (a, b) => (a.original.regions[0]?.region ?? "").localeCompare(b.original.regions[0]?.region ?? ""),
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
		sortFn: "basic",
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
		sortFn: "basic",
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
	page: pageParser.withDefault(0),
};
const TABLE_URL_KEYS = { query: "q" };

export function ResourcesTable({
	items,
	regions,
	orgId,
	workspaceId,
	environmentId,
	envName,
	nowMs,
	hrefFor,
}: {
	items: DashboardItem[];
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
	const sorting = [state.sort];
	const data = filterRows(items, filter);
	const pagination = pageWithin({ pageIndex: state.page, pageSize: PAGE_SIZE }, data.length);
	const table = useDataTable("dashboard-resources", {
		data,
		columns: COLUMNS,
		getRowId: itemKey,
		state: { sorting, pagination },
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) void setState({ sort: next, page: null });
		},
		onPaginationChange: (updater) => {
			void setState({ page: functionalUpdate(updater, pagination).pageIndex });
		},
		manualPagination: false,
		autoResetPageIndex: false,
	});
	const visible = table.getPrePaginatedRowModel().rows.map((row) => row.original);

	const statusCounts = new Map<string, number>();
	const typeCounts = new Map<ResourceType, number>();
	const regionCounts = new Map<string, number>();
	const regionNames = new Set(regions.map((r) => r.region));
	const statusBase = filterRows(items, { ...filter, status: ALL });
	const typeBase = filterRows(items, { ...filter, type: ALL });
	const regionBase = filterRows(items, { ...filter, region: ALL });
	for (const item of statusBase) {
		const key = itemStatusFilter(item);
		statusCounts.set(key, (statusCounts.get(key) ?? 0) + 1);
	}
	for (const item of typeBase) typeCounts.set(itemType(item), (typeCounts.get(itemType(item)) ?? 0) + 1);
	for (const item of items) for (const g of item.regions) regionNames.add(g.region);
	for (const item of regionBase) {
		for (const name of new Set(item.regions.map((g) => g.region))) {
			regionCounts.set(name, (regionCounts.get(name) ?? 0) + 1);
		}
	}
	const typeOptions = [
		{ value: ALL, label: "All types", count: typeBase.length },
		...TYPE_FILTERS.map((o) => ({
			value: o.value,
			label: o.label,
			count: typeCounts.get(o.type) ?? 0,
		})),
	];
	const regionOptions = [
		{ value: ALL, label: "All regions", count: regionBase.length },
		...[...regionNames].map((name) => ({
			value: name,
			label: name,
			count: regionCounts.get(name) ?? 0,
		})),
	];

	const update = (patch: Partial<RowFilter>) => {
		void setState({ ...patch, page: null });
	};
	const hasFilters =
		filter.status !== ALL || filter.type !== ALL || filter.region !== ALL || filter.query.trim() !== "";
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
			<ToggleGroup
				variant="segmented"
				aria-label="Filter resources by status"
				className="max-w-full overflow-x-auto"
				value={[filter.status]}
				onValueChange={(v: string[]) => {
					if (v[0] !== undefined) update({ status: v[0] });
				}}
			>
				<ToggleGroupItem value={ALL} count={statusBase.length}>
					All
				</ToggleGroupItem>
				{visibleStatusFilters(statusCounts, filter.status).map((o) => (
					<ToggleGroupItem key={o.value} value={o.value} count={statusCounts.get(o.value) ?? 0}>
						{o.label}
					</ToggleGroupItem>
				))}
			</ToggleGroup>
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

	const empty = (
		<EmptyState
			icon={<SearchXIcon />}
			title="No resources match"
			query={filter.query.trim()}
			action={
				<Button variant="outline" onClick={clearFilters}>
					<XIcon />
					Clear filters
				</Button>
			}
		/>
	);

	const itemHref = (item: DashboardItem) =>
		isDraftItem(item) ? draftSearch(locationSearch, item.draft.id) : hrefFor(item.resource.id);

	return (
		<Section title="Resources" count={data.length} actions={actions} filters={filters} search={search}>
			{isMap ? (
				visible.length === 0 ? (
					<div className="border-t border-line2">{empty}</div>
				) : (
					<ArchitectureDiagram
						rows={visible}
						regionOrder={regions.map((r) => r.region)}
						environmentId={environmentId}
						hrefFor={itemHref}
						onShowTable={() => {
							void setView("table");
						}}
					/>
				)
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
							trafficLoading: traffic.isLoading,
							trafficError: traffic.error !== null,
						}}
					>
						<DataTable
							table={table}
							inset
							label="Resources"
							onRowClick={(row) => void navigate(itemHref(row.original))}
							rowAction="navigate"
							className="[--row-h:66px]"
							empty={empty}
						/>
					</DashboardCellContext>
					<DataTablePager table={table} pageSizes={[PAGE_SIZE]} empty={`0 of ${items.length.toString()}`} />
				</>
			)}
		</Section>
	);
}
