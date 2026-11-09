import { BoxIcon, SearchXIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { ResourceStatus, type RegionInfo, type ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { SoonTag } from "@/components/design/SoonTag";
import { ResourceStatusBadge, StatusLabel } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { imageVersion } from "@/lib/image";
import { pageRangeLabel, pageSlice } from "@/lib/paging";
import { agoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { ArchitectureDiagram } from "./ArchitectureDiagram";
import type { Draft } from "./drafts";
import { deploymentImage, statusRank } from "./format";
import {
	ALL,
	draftRow,
	filterRows,
	resourceRow,
	STATUS_FILTERS,
	TYPE_FILTERS,
	type ResourceRow,
	type RowFilter,
} from "./resourceRows";
import { ResourceRowMenu } from "./ResourceRowMenu";
import type { EnvResource } from "./useDashboardData";

type SortKey = "name" | "status" | "regions" | "replicas" | "deploy";

const PAGE_SIZE = 25;
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

function compare(key: SortKey, a: EnvResource, b: EnvResource): number {
	switch (key) {
		case "name":
			return a.resource.name.localeCompare(b.resource.name);
		case "status":
			return statusRank(a.status) - statusRank(b.status);
		case "regions":
			return (a.regions[0]?.region ?? "").localeCompare(b.regions[0]?.region ?? "");
		case "replicas":
			return b.totalReplicas - a.totalReplicas;
		case "deploy":
			return tsMs(b.last?.createdAt) - tsMs(a.last?.createdAt);
	}
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

function DataRow({
	row,
	hrefFor,
	onOpenDraft,
	orgId,
	workspaceId,
	envName,
	nowMs,
}: {
	row: ResourceRow;
	hrefFor: (id: string) => string;
	onOpenDraft: (id: string) => void;
	orgId: string;
	workspaceId: string;
	envName: string;
	nowMs: number;
}) {
	const it = row.item;
	const nameClass = "truncate text-left text-[14.5px] font-medium text-foreground underline-offset-3 hover:underline";
	const note = it === undefined ? "" : statusNote(it);
	const last = it?.last;
	const message = last?.message ?? "";
	const degraded = row.status === ResourceStatus.DEGRADED;
	return (
		<TableRow>
			<TableCell>
				<div className="flex min-w-0 flex-col items-start gap-[3px]">
					{row.status === "draft" ? (
						<StatusLabel glyph="draft" label="Draft" />
					) : (
						<ResourceStatusBadge status={row.status} />
					)}
					{note !== "" && (
						<span title={note} className="max-w-full truncate text-[12px] text-fg3">
							{note}
						</span>
					)}
				</div>
			</TableCell>
			<TableCell>
				<div className="flex min-w-0 flex-col gap-0.5">
					{it === undefined ? (
						<button
							type="button"
							className={nameClass}
							onClick={() => {
								onOpenDraft(row.key);
							}}
						>
							{row.name}
						</button>
					) : (
						<Link to={hrefFor(row.key)} className={nameClass}>
							{row.name}
						</Link>
					)}
					<span className="truncate text-[12.5px] text-fg3">{row.description}</span>
				</div>
			</TableCell>
			<TableCell>
				<div className="flex flex-col font-mono text-[12px] leading-[1.6] text-fg2">
					{row.regions.map((g) => (
						<span key={g.region}>{g.region}</span>
					))}
				</div>
			</TableCell>
			<TableCell>
				{it === undefined ? (
					<span className="text-fg4">—</span>
				) : (
					<div className="flex flex-col">
						{row.regions.map((g) => (
							<span key={g.region} className="flex h-[19px] items-center gap-2.5">
								<ReplicaPips desired={g.desired} ready={g.ready} degraded={degraded} />
								<span className="font-mono text-[12px] text-fg2 tabular-nums">
									{g.ready.toString()}/{g.desired.toString()}
								</span>
							</span>
						))}
					</div>
				)}
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
				{it !== undefined && (
					<ResourceRowMenu resource={it.resource} orgId={orgId} workspaceId={workspaceId} envName={envName} />
				)}
			</TableCell>
		</TableRow>
	);
}

export function ResourcesTable({
	items,
	drafts,
	regions,
	orgId,
	workspaceId,
	environmentId,
	envName,
	nowMs,
	hrefFor,
	onOpenDraft,
}: {
	items: EnvResource[];
	drafts: Draft[];
	regions: RegionInfo[];
	orgId: string;
	workspaceId: string;
	environmentId: string;
	envName: string;
	nowMs: number;
	hrefFor: (id: string) => string;
	onOpenDraft: (id: string) => void;
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
	const [sortKey, setSortKey] = useState<SortKey>("status");
	const [sortDir, setSortDir] = useState(1);
	const [page, setPage] = useState(0);

	const sorted = items.toSorted((a, b) => compare(sortKey, a, b) * sortDir);
	const allRows = [...sorted.map(resourceRow), ...drafts.map(draftRow)];
	const visible = filterRows(allRows, filter);
	const { rows: pageRows, page: safePage, pages: pageCount } = pageSlice(visible, page, PAGE_SIZE);

	const statusCounts = new Map<ResourceStatus | "draft", number>();
	const typeCounts = new Map<ResourceType, number>();
	const regionCounts = new Map<string, number>();
	const regionNames = new Set(regions.map((r) => r.region));
	for (const row of allRows) {
		statusCounts.set(row.status, (statusCounts.get(row.status) ?? 0) + 1);
		typeCounts.set(row.type, (typeCounts.get(row.type) ?? 0) + 1);
		for (const name of new Set(row.regions.map((g) => g.region))) {
			regionNames.add(name);
			regionCounts.set(name, (regionCounts.get(name) ?? 0) + 1);
		}
	}
	const typeOptions = [
		{ value: ALL, label: "All", count: allRows.length },
		...TYPE_FILTERS.map((o) => ({ value: o.value, label: o.label, count: typeCounts.get(o.type) ?? 0 })),
	];
	const regionOptions = [
		{ value: ALL, label: "All", count: allRows.length },
		...[...regionNames].map((name) => ({ value: name, label: name, count: regionCounts.get(name) ?? 0 })),
	];

	const update = (patch: Partial<RowFilter>) => {
		setFilter({ ...filter, ...patch });
		setPage(0);
	};
	const hasFilters =
		filter.status !== ALL || filter.type !== ALL || filter.region !== ALL || filter.query.trim() !== "";
	const clearFilters = () => {
		update({ status: ALL, type: ALL, region: ALL, query: "" });
	};

	const title = (
		<>
			Resources
			<Badge tone="count">{allRows.length}</Badge>
		</>
	);
	const actions = (
		<>
			<SearchInput
				value={filter.query}
				placeholder="Search resources"
				aria-label="Search resources"
				className="w-full sm:w-[220px]"
				onChange={(e) => {
					update({ query: e.target.value });
				}}
			/>
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
		</>
	);

	const filters = (
		<div className="flex flex-wrap items-center gap-2.5 px-5 pb-3">
			<ToggleGroup
				variant="segmented"
				aria-label="Filter resources by status"
				className="max-w-full overflow-x-auto"
				value={[filter.status]}
				onValueChange={(v: string[]) => {
					if (v[0] !== undefined) update({ status: v[0] });
				}}
			>
				<ToggleGroupItem value={ALL} count={allRows.length}>
					All
				</ToggleGroupItem>
				{STATUS_FILTERS.map((o) => (
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
		</div>
	);

	const empty =
		allRows.length === 0 ? (
			<EmptyState icon={<BoxIcon />} title={`No resources in ${envName} yet`} />
		) : (
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

	const toggleSort = (key: SortKey) => {
		setSortDir(sortKey === key ? -sortDir : 1);
		setSortKey(key);
	};

	return (
		<Section title={title} actions={actions}>
			{filters}
			{isMap ? (
				visible.length === 0 ? (
					<div className="border-t border-line2">{empty}</div>
				) : (
					<ArchitectureDiagram
						rows={visible}
						regionOrder={regions.map((r) => r.region)}
						environmentId={environmentId}
						hrefFor={hrefFor}
						onOpenDraft={onOpenDraft}
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
									const on = key !== null && sortKey === key;
									return (
										<TableHead
											key={h.label}
											className={h.className}
											aria-sort={on ? (sortDir === 1 ? "ascending" : "descending") : undefined}
										>
											{key === null ? (
												<span className="flex items-center gap-2">
													{h.label}
													{h.soon === true && <SoonTag />}
												</span>
											) : (
												<button
													type="button"
													className="flex items-center gap-1 hover:text-foreground"
													onClick={() => {
														toggleSort(key);
													}}
												>
													{h.label}
													<span className={cn("text-[10px]", on ? "text-foreground" : "text-transparent")}>
														{on && sortDir === -1 ? "▼" : "▲"}
													</span>
												</button>
											)}
										</TableHead>
									);
								})}
								<TableHead className="w-10" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{pageRows.length === 0 && (
								<TableRow>
									<TableCell colSpan={HEADERS.length + 1} className="h-auto p-0 whitespace-normal">
										{empty}
									</TableCell>
								</TableRow>
							)}
							{pageRows.map((row) => (
								<DataRow
									key={row.key}
									row={row}
									hrefFor={hrefFor}
									onOpenDraft={onOpenDraft}
									orgId={orgId}
									workspaceId={workspaceId}
									envName={envName}
									nowMs={nowMs}
								/>
							))}
						</TableBody>
					</Table>
					<Pager
						label={
							visible.length === 0
								? `0 of ${allRows.length.toString()}`
								: pageRangeLabel(safePage, PAGE_SIZE, visible.length)
						}
						onFirst={
							safePage > 0
								? () => {
										setPage(0);
									}
								: null
						}
						onPrev={
							safePage > 0
								? () => {
										setPage(safePage - 1);
									}
								: null
						}
						onNext={
							safePage < pageCount - 1
								? () => {
										setPage(safePage + 1);
									}
								: null
						}
						onLast={
							safePage < pageCount - 1
								? () => {
										setPage(pageCount - 1);
									}
								: null
						}
					/>
				</>
			)}
		</Section>
	);
}
