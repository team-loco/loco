import { BoxIcon, SearchXIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { ResourceStatus, ResourceType, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { effectiveResourceStatus, ResourceStatusBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { pageSlice } from "@/lib/paging";
import { resourcePath } from "@/lib/routes";
import { tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { agoLabel, deploymentImage, imageTag, statusRank } from "./format";
import { ResourceRowMenu } from "./ResourceRowMenu";
import type { EnvResource } from "./useDashboardData";

type SortKey = "name" | "status" | "regions" | "replicas" | "deploy";

const PAGE_SIZE = 25;

const STATUS_OPTIONS: { value: string; status: ResourceStatus; label: string }[] = [
	{ value: "healthy", status: ResourceStatus.HEALTHY, label: "Healthy" },
	{ value: "deploying", status: ResourceStatus.DEPLOYING, label: "Deploying" },
	{ value: "degraded", status: ResourceStatus.DEGRADED, label: "Degraded" },
	{ value: "unavailable", status: ResourceStatus.UNAVAILABLE, label: "Unavailable" },
	{ value: "suspended", status: ResourceStatus.SUSPENDED, label: "Suspended" },
];

const TYPE_OPTIONS: { value: string; type: ResourceType; label: string }[] = [
	{ value: "service", type: ResourceType.SERVICE, label: "Service" },
	{ value: "database", type: ResourceType.DATABASE, label: "Database" },
	{ value: "function", type: ResourceType.FUNCTION, label: "Function" },
	{ value: "cache", type: ResourceType.CACHE, label: "Cache" },
	{ value: "queue", type: ResourceType.QUEUE, label: "Queue" },
	{ value: "blob", type: ResourceType.BLOB, label: "Blob" },
];

const HEADERS: { key: SortKey; label: string; className: string }[] = [
	{ key: "name", label: "Name", className: "w-[22%] min-w-[160px]" },
	{ key: "status", label: "Status", className: "w-[18%] min-w-[130px]" },
	{ key: "regions", label: "Regions", className: "w-[110px]" },
	{ key: "replicas", label: "Replicas", className: "w-[96px]" },
	{ key: "deploy", label: "Last deployment", className: "min-w-[220px]" },
];

function compare(key: SortKey, a: EnvResource, b: EnvResource): number {
	switch (key) {
		case "name":
			return a.resource.name.localeCompare(b.resource.name);
		case "status":
			return (
				statusRank(effectiveResourceStatus(a.resource.status, !a.neverDeployed)) -
				statusRank(effectiveResourceStatus(b.resource.status, !b.neverDeployed))
			);
		case "regions":
			return (a.regions[0]?.region ?? "").localeCompare(b.regions[0]?.region ?? "");
		case "replicas":
			return b.totalReplicas - a.totalReplicas;
		case "deploy":
			return tsMs(b.last?.createdAt) - tsMs(a.last?.createdAt);
	}
}

function statusNote(item: EnvResource): string {
	if (item.neverDeployed) return "Not deployed";
	const { resource, last } = item;
	switch (resource.status) {
		case ResourceStatus.DEPLOYING:
			return last?.message ?? "";
		case ResourceStatus.DEGRADED:
			return resource.regions.find((r) => (r.lastError ?? "") !== "")?.lastError ?? "";
		case ResourceStatus.UNAVAILABLE:
			return resource.regions.find((r) => (r.lastError ?? "") !== "")?.lastError ?? "";
		case ResourceStatus.SUSPENDED:
			return item.totalReplicas === 0 ? "Scaled to 0" : "";
		case ResourceStatus.HEALTHY:
			return "";
		case ResourceStatus.UNSPECIFIED:
			return "";
	}
}

function ReplicaCells({ count, running }: { count: number; running: boolean }) {
	return (
		<div className="flex gap-0.5">
			{Array.from({ length: count }, (_, i) => (
				<span
					key={i}
					className={cn(
						"h-3.5 w-[7px] rounded-[2px] border",
						running ? "border-foreground bg-foreground" : "border-fg4 bg-background",
					)}
				/>
			))}
		</div>
	);
}

export function ResourcesTable({
	items,
	regions,
	orgId,
	workspaceId,
	envName,
	nowMs,
}: {
	items: EnvResource[];
	regions: RegionInfo[];
	orgId: string;
	workspaceId: string;
	envName: string;
	nowMs: number;
}) {
	const [query, setQuery] = useState("");
	const [status, setStatus] = useState("all");
	const [type, setType] = useState("all");
	const [region, setRegion] = useState("all");
	const [sortKey, setSortKey] = useState<SortKey>("status");
	const [sortDir, setSortDir] = useState(1);
	const [page, setPage] = useState(0);

	const q = query.trim().toLowerCase();
	const statusFilter = STATUS_OPTIONS.find((o) => o.value === status);
	const typeFilter = TYPE_OPTIONS.find((o) => o.value === type);
	const visible = items
		.filter(
			(it) =>
				(statusFilter === undefined || it.resource.status === statusFilter.status) &&
				(typeFilter === undefined || it.resource.type === typeFilter.type) &&
				(region === "all" || it.regions.some((g) => g.region === region)) &&
				(q === "" || it.resource.name.toLowerCase().includes(q)),
		)
		.sort((a, b) => compare(sortKey, a, b) * sortDir);

	const { rows: pageItems, page: safePage, pages: pageCount } = pageSlice(visible, page, PAGE_SIZE);

	const regionNames = new Set(regions.map((r) => r.region));
	const statusCounts = new Map<ResourceStatus, number>();
	const typeCounts = new Map<ResourceType, number>();
	const regionCounts = new Map<string, number>();
	for (const it of items) {
		statusCounts.set(it.resource.status, (statusCounts.get(it.resource.status) ?? 0) + 1);
		typeCounts.set(it.resource.type, (typeCounts.get(it.resource.type) ?? 0) + 1);
		const itemRegions = new Set(it.regions.map((g) => g.region));
		for (const name of itemRegions) {
			regionNames.add(name);
			regionCounts.set(name, (regionCounts.get(name) ?? 0) + 1);
		}
	}

	const statusOptions = [
		{ value: "all", label: "All", count: items.length },
		...STATUS_OPTIONS.map((o) => ({ value: o.value, label: o.label, count: statusCounts.get(o.status) ?? 0 })),
	];
	const typeOptions = [
		{ value: "all", label: "All", count: items.length },
		...TYPE_OPTIONS.map((o) => ({ value: o.value, label: o.label, count: typeCounts.get(o.type) ?? 0 })),
	];
	const regionOptions = [
		{ value: "all", label: "All", count: items.length },
		...[...regionNames].map((name) => ({ value: name, label: name, count: regionCounts.get(name) ?? 0 })),
	];

	const hasFilters = status !== "all" || type !== "all" || region !== "all" || q !== "";
	const resetPage = <T,>(set: (v: T) => void) => (v: T) => {
		set(v);
		setPage(0);
	};

	const clearFilters = () => {
		setStatus("all");
		setType("all");
		setRegion("all");
		setQuery("");
		setPage(0);
	};

	const toolbar = (
		<>
			<Input
				value={query}
				placeholder="Search"
				className="w-60"
				onChange={(e) => {
					setQuery(e.target.value);
					setPage(0);
				}}
			/>
			<FilterMenu label="Status" value={status} options={statusOptions} onChange={resetPage(setStatus)} />
			<FilterMenu label="Type" value={type} options={typeOptions} onChange={resetPage(setType)} />
			<FilterMenu label="Region" value={region} options={regionOptions} onChange={resetPage(setRegion)} />
			{hasFilters && (
				<Button
					variant="link"
					className="h-8 px-2"
					onClick={clearFilters}
				>
					Clear filters
				</Button>
			)}
		</>
	);

	const rangeLabel =
		visible.length > PAGE_SIZE
			? `Showing ${(safePage * PAGE_SIZE + 1).toString()}–${(safePage * PAGE_SIZE + pageItems.length).toString()} of ${visible.length.toString()} resources`
			: `Showing ${visible.length.toString()} of ${items.length.toString()} resources`;

	return (
		<Section title="Resources" toolbar={toolbar}>
			<Table className="min-w-[860px] table-fixed text-base">
				<TableHeader>
					<TableRow className="h-10 border-line hover:bg-transparent">
						{HEADERS.map((h) => {
							const on = sortKey === h.key;
							return (
								<TableHead key={h.key} className={cn("h-10 px-0 first:pl-4", h.className)}>
									<button
										type="button"
										className="flex items-center gap-1 text-sm font-semibold text-fg2"
										onClick={() => {
											setSortDir(on ? -sortDir : 1);
											setSortKey(h.key);
										}}
									>
										{h.label}
										<span className={cn("text-[10px]", on ? "text-foreground" : "text-transparent")}>
											{on && sortDir === -1 ? "▼" : "▲"}
										</span>
									</button>
								</TableHead>
							);
						})}
						<TableHead className="h-10 w-12 pr-4" />
					</TableRow>
				</TableHeader>
				<TableBody>
					{pageItems.length === 0 && (
						<TableRow className="hover:bg-transparent">
							<TableCell colSpan={6} className="p-0 whitespace-normal">
								{items.length === 0 ? (
									<EmptyState icon={<BoxIcon />} title={`No resources in ${envName} yet`} />
								) : (
									<EmptyState
										icon={<SearchXIcon />}
										title="No resources match"
										query={query.trim()}
										action={
											<Button variant="outline" onClick={clearFilters}>
												<XIcon />
												Clear filters
											</Button>
										}
									/>
								)}
							</TableCell>
						</TableRow>
					)}
					{pageItems.map((it) => {
						const { resource, last } = it;
						const note = statusNote(it);
						const tag = last !== undefined ? imageTag(deploymentImage(last)) : "";
						const message = last?.message ?? "";
						return (
							<TableRow key={resource.id} className="border-line hover:bg-bg2">
								<TableCell className="py-1.5 pr-3 pl-4">
									<div className="flex min-w-0 flex-col gap-px">
										<Link
											to={resourcePath(orgId, workspaceId, resource.id)}
											className="truncate text-md font-semibold text-foreground"
										>
											{resource.name}
										</Link>
										<span className="truncate text-sm text-fg3">{resource.description ?? ""}</span>
									</div>
								</TableCell>
								<TableCell className="py-1.5 pr-3 pl-0">
									<div className="flex min-w-0 flex-col items-start gap-[3px]">
										<ResourceStatusBadge status={effectiveResourceStatus(resource.status, !it.neverDeployed)} />
										{note !== "" && <span className="max-w-full truncate text-sm text-fg3">{note}</span>}
									</div>
								</TableCell>
								<TableCell className="py-1.5 pr-3 pl-0">
									<div className="flex flex-col gap-px text-[12.5px] text-fg2">
										{it.regions.map((g) => (
											<span key={g.region}>{g.region}</span>
										))}
									</div>
								</TableCell>
								<TableCell className="py-1.5 pr-3 pl-0">
									<div className="flex flex-col justify-center gap-[3px]">
										{it.regions.map((g) => (
											<div key={g.region} className="flex h-[15px] items-center gap-2">
												<ReplicaCells count={g.replicas} running={g.running} />
												<span className="text-sm text-fg3 tabular-nums">
													{g.replicas === 0 ? "0" : g.replicas.toString()}
												</span>
											</div>
										))}
									</div>
								</TableCell>
								<TableCell className="py-1.5 pr-3 pl-0">
									{last === undefined ? (
										<span className="text-fg3">—</span>
									) : (
										<div className="flex min-w-0 flex-col gap-px">
											<span className="truncate">
												{tag} <span className="text-fg3">· {agoLabel(tsMs(last.createdAt), nowMs)}</span>
											</span>
											<span className={cn("truncate text-sm", message !== "" ? "text-fg2" : "text-fg3")}>
												{message !== "" ? message : "Running"}
											</span>
										</div>
									)}
								</TableCell>
								<TableCell className="py-1.5 pr-4 pl-0 text-right">
									<ResourceRowMenu
										resource={resource}
										orgId={orgId}
										workspaceId={workspaceId}
										envName={envName}
									/>
								</TableCell>
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
			<Pager
				label={rangeLabel}
				buttonSize="sm"
				className="py-2.5"
				onPrev={safePage > 0 ? () => { setPage(safePage - 1); } : null}
				onNext={safePage < pageCount - 1 ? () => { setPage(safePage + 1); } : null}
			/>
		</Section>
	);
}
