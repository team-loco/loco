import { useTable, type SortingState } from "@tanstack/react-table";
import { sortingFeatures, SortableTableHead, type SortColumn } from "@/components/design/DataTable";
import {
	ActivityIcon,
	ArrowRightIcon,
	ChevronsUpDownIcon,
	ScrollTextIcon,
	SearchXIcon,
	VariableIcon,
	XIcon,
} from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { ResourceStatus, type RegionInfo, type Resource, type ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { ResourceRowMenu } from "@/components/ResourceRowMenu";
import { Button } from "@/components/design/Button";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { SoonTag } from "@/components/design/SoonTag";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { deploymentImage } from "@/lib/deployment-utils";
import { DRAFT_NOTE } from "@/lib/drafts";
import { imageVersion } from "@/lib/image";
import { usePagedRows } from "@/lib/paging";
import { draftSearch, observabilityPath, resourcePath, resourcesPath } from "@/lib/routes";
import { agoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { ArchitectureDiagram } from "./ArchitectureDiagram";
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
	type DraftItem,
	type RowFilter,
} from "./resourceRows";
import type { EnvResource } from "./useDashboardData";

type SortKey = "name" | "status" | "regions" | "replicas" | "deploy";

const PAGE_SIZES = [25] as const;
const MAX_PIPS = 8;
const VIEW_PARAM = "view";
const MAP_VIEW = "map";
const TABLE_VIEW = "table";

const HEADERS: { key: SortKey | null; label: string; className: string; soon?: boolean }[] = [
	{ key: "status", label: "Status", className: "w-[130px]" },
	{ key: "name", label: "Resource", className: "" },
	{ key: "regions", label: "Region", className: "w-[100px]" },
	{ key: "replicas", label: "Replicas", className: "w-[110px]" },
	{ key: null, label: "Traffic", className: "w-[90px]", soon: true },
	{ key: null, label: "Last deploy", className: "" },
	{ key: "deploy", label: "Deployed", className: "w-[84px] text-right [&>button]:ml-auto" },
];

function replicaTotal(item: DashboardItem): number {
	return isDraftItem(item) ? 0 : item.totalReplicas;
}

function lastDeployMs(item: DashboardItem): number {
	return isDraftItem(item) ? 0 : tsMs(item.last?.createdAt);
}

const SORT_COLUMNS: SortColumn<DashboardItem>[] = [
	{
		id: "name",
		accessorFn: itemName,
		sortFn: (a, b) => itemName(a.original).localeCompare(itemName(b.original)),
		sortDescFirst: false,
	},
	{ id: "status", accessorFn: itemRank, sortFn: "basic", sortDescFirst: false },
	{
		id: "regions",
		accessorFn: (item) => item.regions[0]?.region ?? "",
		sortFn: (a, b) => (a.original.regions[0]?.region ?? "").localeCompare(b.original.regions[0]?.region ?? ""),
		sortDescFirst: false,
	},
	{ id: "replicas", accessorFn: replicaTotal, sortFn: "basic", sortDescFirst: true },
	{ id: "deploy", accessorFn: lastDeployMs, sortFn: "basic", sortDescFirst: true },
];

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

function DashboardRowMenu({
	resource,
	orgId,
	workspaceId,
	envName,
}: {
	resource: Resource;
	orgId: string;
	workspaceId: string;
	envName: string;
}) {
	const navigate = useNavigate();
	const base = resourcePath(orgId, workspaceId, resource.id);
	const envQuery = `env=${encodeURIComponent(envName)}`;
	const obs = (view: "logs" | "events") =>
		observabilityPath(orgId, workspaceId, view, { env: envName, r: resource.name });
	return (
		<ResourceRowMenu resource={resource} workspaceId={workspaceId}>
			<DropdownMenuItem onClick={() => void navigate(obs("logs"))}>
				<ScrollTextIcon className="text-fg3" />
				View logs
			</DropdownMenuItem>
			<DropdownMenuItem onClick={() => void navigate(obs("events"))}>
				<ActivityIcon className="text-fg3" />
				View events
			</DropdownMenuItem>
			<DropdownMenuItem onClick={() => void navigate(`${base}?tab=settings&${envQuery}`)}>
				<ChevronsUpDownIcon className="text-fg3" />
				Scale
			</DropdownMenuItem>
			<DropdownMenuItem onClick={() => void navigate(`${base}?tab=variables&${envQuery}`)}>
				<VariableIcon className="text-fg3" />
				Environment variables
			</DropdownMenuItem>
		</ResourceRowMenu>
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

function DraftDataRow({ item, href }: { item: DraftItem; href: string }) {
	return (
		<TableRow>
			<TableCell>
				<span className="inline-flex items-center gap-2 text-[13.5px] whitespace-nowrap text-foreground">
					<StatusGlyph glyph="draft" />
					Draft
				</span>
			</TableCell>
			<TableCell>
				<div className="flex min-w-0 flex-col gap-0.5">
					<Link to={href} className={cn(NAME_CLASS, "underline decoration-fg4 decoration-dashed")}>
						{item.draft.name}
					</Link>
					<span className="truncate text-[12.5px] text-fg3">{DRAFT_NOTE}</span>
				</div>
			</TableCell>
			<TableCell>
				<Regions item={item} />
			</TableCell>
			<TableCell className="font-mono text-[12px] text-fg4">—</TableCell>
			<TableCell />
			<TableCell>
				<span className="block truncate font-mono text-[12px] text-fg3" title={item.draft.image}>
					{item.draft.image}
				</span>
			</TableCell>
			<TableCell className="text-right font-mono text-[12px] text-fg3">—</TableCell>
			<TableCell />
		</TableRow>
	);
}

function DataRow({
	item,
	hrefFor,
	orgId,
	workspaceId,
	envName,
	nowMs,
}: {
	item: EnvResource;
	hrefFor: (id: string) => string;
	orgId: string;
	workspaceId: string;
	envName: string;
	nowMs: number;
}) {
	const note = statusNote(item);
	const last = item.last;
	const message = last?.message ?? "";
	const degraded = item.status === ResourceStatus.DEGRADED;
	return (
		<TableRow>
			<TableCell>
				<div className="flex min-w-0 flex-col items-start gap-[3px]">
					<ResourceStatusBadge status={item.status} />
					{note !== "" && (
						<span title={note} className="max-w-full truncate text-[12px] text-fg3">
							{note}
						</span>
					)}
				</div>
			</TableCell>
			<TableCell>
				<div className="flex min-w-0 flex-col gap-0.5">
					<Link to={hrefFor(item.resource.id)} className={NAME_CLASS}>
						{item.resource.name}
					</Link>
					<span className="truncate text-[12.5px] text-fg3">{item.resource.description ?? ""}</span>
				</div>
			</TableCell>
			<TableCell>
				<Regions item={item} />
			</TableCell>
			<TableCell>
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
			</TableCell>
			<TableCell />
			<TableCell>
				{last === undefined ? (
					<span className="text-fg4">—</span>
				) : (
					<div className="flex min-w-0 flex-col gap-0.5">
						<span className="truncate font-mono text-[12px]">{imageVersion(deploymentImage(last))}</span>
						<span className="truncate text-[12.5px] text-fg3">{message !== "" ? message : "Running"}</span>
					</div>
				)}
			</TableCell>
			<TableCell className="text-right font-mono text-[12px] whitespace-nowrap text-fg3">
				{last === undefined ? "—" : agoLabel(tsMs(last.createdAt), nowMs)}
			</TableCell>
			<TableCell className="text-right">
				<DashboardRowMenu resource={item.resource} orgId={orgId} workspaceId={workspaceId} envName={envName} />
			</TableCell>
		</TableRow>
	);
}

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
	const table = useTable({
		features: sortingFeatures,
		data: filterRows(items, filter),
		columns: SORT_COLUMNS,
		state: { sorting },
		onSortingChange: setSorting,
		enableSortingRemoval: false,
		enableMultiSort: false,
	});
	const visible = table.getRowModel().rows.map((row) => row.original);
	const paged = usePagedRows(visible, PAGE_SIZES, `0 of ${items.length.toString()}`);

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
		paged.reset();
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
					<Table className="min-w-[820px] table-fixed">
						<TableHeader>
							<TableRow>
								{HEADERS.map((h) => {
									const key = h.key;
									return key === null ? (
										<TableHead key={h.label} className={h.className}>
											<span className="flex items-center gap-2">
												{h.label}
												{h.soon === true && <SoonTag />}
											</span>
										</TableHead>
									) : (
										<SortableTableHead
											key={h.label}
											column={table.getColumn(key)}
											direction={sorting[0]?.id === key ? (sorting[0].desc ? "desc" : "asc") : false}
											className={h.className}
										>
											{h.label}
										</SortableTableHead>
									);
								})}
								<TableHead className="w-10" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{paged.rows.length === 0 && (
								<TableRow>
									<TableCell colSpan={HEADERS.length + 1} className="h-auto p-0 whitespace-normal">
										{empty}
									</TableCell>
								</TableRow>
							)}
							{paged.rows.map((item) =>
								isDraftItem(item) ? (
									<DraftDataRow key={itemKey(item)} item={item} href={itemHref(item)} />
								) : (
									<DataRow
										key={itemKey(item)}
										item={item}
										hrefFor={hrefFor}
										orgId={orgId}
										workspaceId={workspaceId}
										envName={envName}
										nowMs={nowMs}
									/>
								),
							)}
						</TableBody>
					</Table>
					<Pager {...paged.pager} {...paged.ends} />
				</>
			)}
		</Section>
	);
}
