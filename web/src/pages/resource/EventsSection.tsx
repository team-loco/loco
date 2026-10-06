import { useQuery } from "@connectrpc/connect-query";
import { ChevronDownIcon } from "lucide-react";
import { useState } from "react";
import { listResourceEvents } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Event } from "@gen/loco/resource/v1/resource_pb";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { pageRangeLabel, pageSlice } from "@/lib/paging";
import { maybeTsMs, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { formatFullTime } from "./format";
import { PAGE_SIZES } from "./model";

type Severity = "error" | "warning" | "normal";
type TypeFilter = "all" | Severity;

const ERROR_REASONS = new Set(["BackOff", "OOMKilling", "Failed", "FailedScheduling"]);

const TYPE_FILTERS: { value: TypeFilter; label: string }[] = [
	{ value: "all", label: "All" },
	{ value: "error", label: "Errors" },
	{ value: "warning", label: "Warnings" },
	{ value: "normal", label: "Normal" },
];

const GRID = "grid grid-cols-[140px_64px_130px_minmax(0,1fr)_200px] items-center gap-3 px-4";

function severityOf(e: Event): Severity {
	if (e.type !== "Warning") return "normal";
	return ERROR_REASONS.has(e.reason) ? "error" : "warning";
}

function severityBadge(s: Severity): { label: string; tone: BadgeTone } {
	switch (s) {
		case "error":
			return { label: "Error", tone: "bad" };
		case "warning":
			return { label: "Warning", tone: "warn" };
		case "normal":
			return { label: "Normal", tone: "neutral" };
	}
}

export function EventsSection({ resourceId, multiRegion }: { resourceId: string; multiRegion: boolean }) {
	const [query, setQuery] = useState("");
	const [type, setType] = useState<TypeFilter>("all");
	const [page, setPage] = useState(0);
	const [size, setSize] = useState(10);

	const { data, isLoading, error } = useQuery(
		listResourceEvents,
		{ resourceId, limit: 500 },
		{ enabled: resourceId !== "", refetchInterval: 30_000 },
	);
	const events = (data?.events ?? []).toSorted((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));

	const q = query.trim().toLowerCase();
	const base = events.filter((e) => q === "" || `${e.reason} ${e.message} ${e.podName}`.toLowerCase().includes(q));
	const severityCounts: Record<TypeFilter, number> = { all: base.length, error: 0, warning: 0, normal: 0 };
	const filtered: Event[] = [];
	for (const e of base) {
		const severity = severityOf(e);
		severityCounts[severity]++;
		if (type === "all" || severity === type) filtered.push(e);
	}
	const slice = pageSlice(filtered, page, size);
	const hasFilters = q !== "" || type !== "all";

	if (isUnimplemented(error)) {
		return (
			<Section
				className="scroll-mt-4"
				title={
					<span id="events" className="flex items-baseline gap-2.5">
						Events
						<SoonTag />
					</span>
				}
			>
				<EmptyState title="Events aren't available yet">
					Kubernetes events for this resource will show up here once the API supports them.
				</EmptyState>
			</Section>
		);
	}

	return (
		<Section
			className="scroll-mt-4"
			title={
				<span id="events" className="flex items-baseline gap-2.5">
					Events
					<span className="text-sm font-normal text-fg3">{events.length} total</span>
				</span>
			}
		>
			<div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
				<SearchInput
					size="sm"
					value={query}
					onChange={(e) => {
						setQuery(e.target.value);
						setPage(0);
					}}
					placeholder="Search reason, message, replica"
					className="w-60"
				/>
				<ToggleGroup
					variant="segmented"
					value={[type]}
					onValueChange={(v: string[]) => {
						const next = TYPE_FILTERS.find((t) => t.value === v[0]);
						if (next !== undefined) {
							setType(next.value);
							setPage(0);
						}
					}}
				>
					{TYPE_FILTERS.map((t) => (
						<ToggleGroupItem key={t.value} value={t.value} className="h-6! gap-1.5 px-2.5 text-sm">
							{t.label}
							<span className="text-xs font-normal text-fg3 tabular-nums">{severityCounts[t.value]}</span>
						</ToggleGroupItem>
					))}
				</ToggleGroup>
				<Button variant="outline" className="h-[30px] cursor-not-allowed gap-1.5 px-2 text-[12.5px]" disabled>
					All deployments
					<ChevronDownIcon className="size-3 text-fg3" />
					<SoonTag />
				</Button>
				{multiRegion && (
					<Button variant="outline" className="h-[30px] cursor-not-allowed gap-1.5 px-2 text-[12.5px]" disabled>
						All regions
						<ChevronDownIcon className="size-3 text-fg3" />
						<SoonTag />
					</Button>
				)}
				{hasFilters && (
					<Button
						variant="ghost"
						className="h-[30px] px-2 text-sm text-link hover:bg-transparent"
						onClick={() => {
							setQuery("");
							setType("all");
							setPage(0);
						}}
					>
						Clear
					</Button>
				)}
			</div>
			<div className="overflow-x-auto">
				<div className="min-w-[760px]">
					<div className={cn(GRID, "h-9 border-b border-line text-sm font-semibold text-fg2")}>
						<span>Time</span>
						<span>Type</span>
						<span>Reason</span>
						<span>Message</span>
						<span>Replica</span>
					</div>
					{isLoading &&
						[0, 1, 2, 3].map((i) => (
							<div key={i} className={cn(GRID, "h-[38px] border-b border-line")}>
								<Skeleton className="h-3.5 w-28" />
								<Skeleton className="h-4 w-12" />
								<Skeleton className="h-3.5 w-20" />
								<Skeleton className="h-3.5 w-full" />
								<Skeleton className="h-3.5 w-32" />
							</div>
						))}
					{error !== null && (
						<div className="px-4 py-5 text-bad-fg">{getErrorMessage(error, "Failed to load events")}</div>
					)}
					{!isLoading &&
						slice.rows.map((e, i) => {
							const sev = severityBadge(severityOf(e));
							const ms = maybeTsMs(e.timestamp);
							return (
								<div key={`${String(ms)}-${String(i)}`} className={cn(GRID, "h-[38px] border-b border-line")}>
									<span className="text-fg3 tabular-nums">{ms === undefined ? "—" : formatFullTime(ms)}</span>
									<Badge tone={sev.tone} size="sm">
										{sev.label}
									</Badge>
									<span className="truncate font-medium" title={e.reason}>
										{e.reason}
									</span>
									<span className="truncate text-fg2" title={e.message}>
										{e.message}
									</span>
									<span className="truncate text-fg3" title={e.podName}>
										{e.podName === "" ? "—" : e.podName}
									</span>
								</div>
							);
						})}
					{!isLoading && error === null && filtered.length === 0 && (
						<div className="px-4 py-5 text-fg3">
							{events.length === 0 ? "No events recorded for this resource." : "No events match these filters."}
						</div>
					)}
				</div>
			</div>
			<Pager
				label={filtered.length > 0 ? pageRangeLabel(slice.page, size, filtered.length) : "0 of 0"}
				pageSizes={PAGE_SIZES}
				pageSize={size}
				onPageSize={(n) => {
					setSize(n);
					setPage(0);
				}}
				onPrev={slice.page > 0 ? () => { setPage(slice.page - 1); } : null}
				onNext={slice.page < slice.pages - 1 ? () => { setPage(slice.page + 1); } : null}
			/>
		</Section>
	);
}
