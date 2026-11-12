import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { HOUR_MS } from "@/lib/obs-time";
import { TRAFFIC_METRICS, latestTraffic, trafficPoints, trafficValue, type TrafficPoint } from "@/lib/traffic";
import { useTable, type PaginationState, type SortingState } from "@tanstack/react-table";
import { ArrowRightIcon, SearchXIcon, XIcon } from "lucide-react";
import { createContext, use, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { ResourceStatus, type RegionInfo, type ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import {
	DataTable,
	dataTableFeatures,
	sortOptions,
	DataTablePager,
	pageWithin,
	type DataColumn,
} from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { deploymentImage } from "@/lib/deployment-utils";
import { DRAFT_NOTE } from "@/lib/drafts";
import { imageVersion } from "@/lib/image";
import { draftSearch, resourcesPath } from "@/lib/routes";
import { agoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

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
	statusOptions,
	TYPE_FILTERS,
	viewAllSearch,
	type DashboardItem,
	type RowFilter,
} from "./resourceRows";
import type { EnvResource } from "./useDashboardData";

const PAGE_SIZE = 25;
const MAX_PIPS = 8;
const VIEW_PARAM = "view";
const MAP_VIEW = "map";
const TABLE_VIEW = "table";

function replicaTotal(item: DashboardItem): number {
	return isDraftItem(item) ? 0 : item.totalReplicas;
}

function lastDeployMs(item: DashboardItem): number {
	return isDraftItem(item) ? 0 : tsMs(item.last?.createdAt);
}

function regionNote(item: EnvResource): string {
	return item.environment?.regions.find((r) => (r.message ?? "") !== "")?.message ?? "";
}

function statusNote(item: EnvResource): string {
	if (item.neverDeployed) return "Not deployed";
	switch (item.status) {
		case ResourceStatus.DEPLOYING:
			return item.last?.message ?? "";
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
		<span className="flex gap-[3px]" aria-hidden="true">
			{Array.from({ length: Math.min(desired, MAX_PIPS) }, (_, i) => (
				<span
					key={i}
					className={cn(
						"h-3.5 w-1.5 rounded-[1.5px]",
						i >= ready ? "ring-1 ring-line2 ring-inset" : degraded ? "bg-warn" : "bg-foreground",
					)}
				/>
			))}
		</span>
	);
}

const NAME_CLASS = "truncate text-left text-[14.5px] font-medium text-foreground underline-offset-3 hover:underline";

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
		return (
			<span className="inline-flex items-center gap-2 text-[13.5px] whitespace-nowrap text-foreground">
				<StatusGlyph glyph="draft" />
				Draft
			</span>
		);
	}
	const note = statusNote(item);
	return (
		<div className="flex min-w-0 flex-col items-start gap-[3px]">
			<ResourceStatusBadge status={item.status} />
			{note !== "" && (
				<span title={note} className="max-w-full truncate text-[12px] text-fg3">
					{note}
				</span>
			)}
		</div>
	);
}

function NameCell({ item }: { item: DashboardItem }) {
	const { hrefFor } = useCells();
	const draft = isDraftItem(item);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<Link to={hrefFor(item)} className={cn(NAME_CLASS, draft && "underline decoration-fg4 decoration-dashed")}>
				{itemName(item)}
			</Link>
			<span className="truncate text-[12.5px] text-fg3">{draft ? DRAFT_NOTE : (item.resource.description ?? "")}</span>
		</div>
	);
}

function ReplicasCell({ item }: { item: DashboardItem }) {
	if (isDraftItem(item)) return <span className="font-mono text-[12px] text-fg4">—</span>;
	const degraded = item.status === ResourceStatus.DEGRADED;
	return (
		<div className="flex flex-col">
			{item.regions.map((g) => (
				<span key={g.region} className="flex h-[19px] items-center gap-2.5">
					<ReplicaPips desired={g.desired} ready={g.ready} degraded={degraded} />
					<span className="font-mono text-[12px] text-fg2 tabular-nums">
						{g.ready.toString()}/{g.desired.toString()}
					</span>
				</span>
			))}
		</div>
	);
}

function LastDeployCell({ item }: { item: DashboardItem }) {
	if (isDraftItem(item)) {
		return (
			<span className="block truncate font-mono text-[12px] text-fg3" title={item.draft.image}>
				{item.draft.image}
			</span>
		);
	}
	const last = item.last;
	if (last === undefined) return <span className="text-fg4">—</span>;
	const message = last.message;
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<span className="truncate font-mono text-[12px]">{imageVersion(deploymentImage(last))}</span>
			<span className="truncate text-[12.5px] text-fg3">{message !== "" ? message : "Running"}</span>
		</div>
	);
}

function DeployedCell({ item }: { item: DashboardItem }) {
	const { nowMs } = useCells();
	const last = isDraftItem(item) ? undefined : item.last;
	return <>{last === undefined ? "—" : agoLabel(tsMs(last.createdAt), nowMs)}</>;
}

function MenuCell({ item }: { item: DashboardItem }) {
	const { orgId, workspaceId, envName } = useCells();
	if (isDraftItem(item)) return null;
	return <DashboardRowMenu item={item} orgId={orgId} workspaceId={workspaceId} envName={envName} />;
}

function TrafficCell({ item }: { item: DashboardItem }) {
	const { traffic, trafficLoading, trafficError } = useCells();
	if (isDraftItem(item)) return <span className="text-fg3">—</span>;
	const point = latestTraffic(traffic, item.resource.id);
	return (
		<span
			className="font-mono text-[12px] whitespace-nowrap"
			title={trafficError ? "Failed to load traffic" : "Latest request rate"}
		>
			{trafficLoading ? "…" : trafficError ? "Unavailable" : trafficValue(point?.requests, "/s")}
		</span>
	);
}

export const COLUMNS: DataColumn<DashboardItem>[] = [
	{
		id: "status",
		header: "Status",
		accessorFn: itemRank,
		sortFn: "basic",
		meta: { headClassName: "w-[130px]" },
		cell: ({ row }) => <StatusCell item={row.original} />,
	},
	{
		id: "name",
		header: "Resource",
		accessorFn: itemName,
		sortFn: (a, b) => itemName(a.original).localeCompare(itemName(b.original)),
		cell: ({ row }) => <NameCell item={row.original} />,
	},
	{
		id: "regions",
		header: "Region",
		accessorFn: (item) => item.regions[0]?.region ?? "",
		sortFn: (a, b) => (a.original.regions[0]?.region ?? "").localeCompare(b.original.regions[0]?.region ?? ""),
		meta: { headClassName: "w-[100px]" },
		cell: ({ row }) => <Regions item={row.original} />,
	},
	{
		id: "replicas",
		header: "Replicas",
		accessorFn: replicaTotal,
		sortFn: "basic",
		sortDescFirst: true,
		meta: { headClassName: "w-[110px]" },
		cell: ({ row }) => <ReplicasCell item={row.original} />,
	},
	{
		id: "traffic",
		header: "Traffic",
		enableSorting: false,
		meta: { headClassName: "w-[90px]" },
		cell: ({ row }) => <TrafficCell item={row.original} />,
	},
	{
		id: "last",
		header: "Last deploy",
		enableSorting: false,
		cell: ({ row }) => <LastDeployCell item={row.original} />,
	},
	{
		id: "deploy",
		header: "Deployed",
		accessorFn: lastDeployMs,
		sortFn: "basic",
		sortDescFirst: true,
		meta: {
			headClassName: "w-[84px] text-right [&>button]:ml-auto",
			cellClassName: "text-right font-mono text-[12px] whitespace-nowrap text-fg3",
		},
		cell: ({ row }) => <DeployedCell item={row.original} />,
	},
	{
		id: "menu",
		enableSorting: false,
		meta: { headClassName: "w-10", cellClassName: "text-right" },
		cell: ({ row }) => <MenuCell item={row.original} />,
	},
];

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
	const [params, setParams] = useSearchParams();
	const isMap = params.get(VIEW_PARAM) === MAP_VIEW;
	const setView = (view: string) => {
		const next = new URLSearchParams(params);
		if (view === MAP_VIEW) next.set(VIEW_PARAM, MAP_VIEW);
		else next.delete(VIEW_PARAM);
		setParams(next, { replace: true });
	};
	const [filter, setFilter] = useState<RowFilter>({ status: ALL, type: ALL, region: ALL, query: "" });
	const [sorting, setSorting] = useState<SortingState>([{ id: "status", desc: false }]);
	const [paging, setPaging] = useState<PaginationState>({ pageIndex: 0, pageSize: PAGE_SIZE });
	const data = filterRows(items, filter);
	const pagination = pageWithin(paging, data.length);
	const table = useTable({
		...sortOptions,
		features: dataTableFeatures,
		data,
		columns: COLUMNS,
		getRowId: itemKey,
		state: { sorting, pagination },
		onSortingChange: setSorting,
		onPaginationChange: setPaging,
		enableColumnResizing: false,
		autoResetPageIndex: false,
	});
	const visible = table.getPrePaginatedRowModel().rows.map((row) => row.original);

	const statusCounts = new Map<ResourceStatus, number>();
	const typeCounts = new Map<ResourceType, number>();
	const regionCounts = new Map<string, number>();
	const regionNames = new Set(regions.map((r) => r.region));
	for (const item of items) {
		if (!isDraftItem(item)) statusCounts.set(item.status, (statusCounts.get(item.status) ?? 0) + 1);
		typeCounts.set(itemType(item), (typeCounts.get(itemType(item)) ?? 0) + 1);
		for (const name of new Set(item.regions.map((g) => g.region))) {
			regionNames.add(name);
			regionCounts.set(name, (regionCounts.get(name) ?? 0) + 1);
		}
	}
	const typeOptions = [
		{ value: ALL, label: "All", count: items.length },
		...TYPE_FILTERS.map((o) => ({ value: o.value, label: o.label, count: typeCounts.get(o.type) ?? 0 })),
	];
	const regionOptions = [
		{ value: ALL, label: "All", count: items.length },
		...[...regionNames].map((name) => ({ value: name, label: name, count: regionCounts.get(name) ?? 0 })),
	];

	const update = (patch: Partial<RowFilter>) => {
		setFilter({ ...filter, ...patch });
		setPaging({ ...pagination, pageIndex: 0 });
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
				value={[isMap ? MAP_VIEW : TABLE_VIEW]}
				onValueChange={(v: string[]) => {
					if (v[0] !== undefined) setView(v[0]);
				}}
			>
				<ToggleGroupItem value={TABLE_VIEW}>Table</ToggleGroupItem>
				<ToggleGroupItem value={MAP_VIEW}>Map</ToggleGroupItem>
			</ToggleGroup>
			<Button
				variant="outline"
				size="sm"
				nativeButton={false}
				render={<Link to={`${resourcesPath(orgId, workspaceId)}?${viewAllSearch(envName, filter)}`} />}
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
				<ToggleGroupItem value={ALL} count={items.length}>
					All
				</ToggleGroupItem>
				{statusOptions(statusCounts, filter.status).map((o) => (
					<ToggleGroupItem key={o.value} value={o.value} count={statusCounts.get(o.status) ?? 0}>
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
				value={filter.region}
				options={regionOptions}
				onChange={(region) => {
					update({ region });
				}}
			/>
			{hasFilters && (
				<Button variant="link" className="px-1" onClick={clearFilters}>
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
		isDraftItem(item) ? draftSearch(params, item.draft.id) : hrefFor(item.resource.id);

	return (
		<Section title="Resources" count={items.length} actions={actions} filters={filters} search={search}>
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
							setView(TABLE_VIEW);
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
						<DataTable table={table} className="min-w-[820px] table-fixed" empty={empty} />
					</DashboardCellContext>
					<DataTablePager table={table} pageSizes={[PAGE_SIZE]} empty={`0 of ${items.length.toString()}`} />
				</>
			)}
		</Section>
	);
}
