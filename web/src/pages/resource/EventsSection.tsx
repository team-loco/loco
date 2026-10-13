import { ActivityIcon, SearchXIcon, XIcon } from "lucide-react";
import { useState } from "react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { pageRangeLabel, pageSlice } from "@/lib/paging";
import { maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { filterEvents, objectLabel, severityOf, type EventsTarget, type Severity, type TypeFilter } from "./events";
import { formatFullTime } from "./format";
import { PAGE_SIZES } from "./model";
import { useResourceEvents } from "./useResourceEvents";

const DEFAULT_PAGE_SIZE = 10;
const SKELETON_ROWS = [0, 1, 2, 3];

const TYPE_FILTERS: { value: TypeFilter; label: string }[] = [
	{ value: "all", label: "All" },
	{ value: "warning", label: "Warnings" },
	{ value: "normal", label: "Normal" },
];

const GRID = "lg:grid-cols-[140px_72px_150px_minmax(0,1fr)_56px_220px]";
const ROW = cn(
	GRID,
	"grid min-h-[38px] grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-b border-line px-4 py-2 lg:py-0",
);
const WIDE = "col-span-3 lg:col-span-1";
const COUNT = "col-start-3 row-start-1 text-right lg:col-start-auto lg:row-start-auto";

function severityBadge(s: Severity): { label: string; tone: BadgeTone } {
	switch (s) {
		case "warning":
			return { label: "Warning", tone: "warn" };
		case "normal":
			return { label: "Normal", tone: "neutral" };
	}
}

export function EventsSection({
	target,
	resourceName,
	deployments,
}: {
	target: EventsTarget;
	resourceName: string;
	deployments: Deployment[];
}) {
	const [query, setQuery] = useState("");
	const [type, setType] = useState<TypeFilter>("all");
	const [page, setPage] = useState(0);
	const [size, setSize] = useState(DEFAULT_PAGE_SIZE);

	const { events, isLoading, unavailable, error } = useResourceEvents(target, deployments);
	const { rows, counts } = filterEvents(events, query, type);
	const slice = pageSlice(rows, page, size);
	const hasFilters = query.trim() !== "" || type !== "all";
	const clearFilters = () => {
		setQuery("");
		setType("all");
		setPage(0);
	};

	const title = (
		<span id="events" className="flex items-baseline gap-2.5">
			Events
			<span className="text-sm font-normal text-fg3">{events.length} in the last 24h</span>
		</span>
	);

	if (unavailable) {
		return (
			<Section className="scroll-mt-4" title={title}>
				<EmptyState icon={<ActivityIcon />} title={`No events for ${resourceName}`}>
					Events show up here once {resourceName} runs in a region with an observability proxy.
				</EmptyState>
			</Section>
		);
	}

	return (
		<Section className="scroll-mt-4" title={title}>
			<div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
				<SearchInput
					size="sm"
					value={query}
					onChange={(e) => {
						setQuery(e.target.value);
						setPage(0);
					}}
					placeholder="Search reason, message, object"
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
							<span className="text-xs font-normal text-fg3 tabular-nums">{counts[t.value]}</span>
						</ToggleGroupItem>
					))}
				</ToggleGroup>
				{hasFilters && (
					<Button variant="ghost" className="h-[30px] px-2 text-sm text-link hover:bg-transparent" onClick={clearFilters}>
						Clear
					</Button>
				)}
			</div>
			<div className="overflow-x-auto">
				<div className="lg:min-w-[860px]">
					<div
						className={cn(
							GRID,
							"hidden h-9 items-center gap-3 border-b border-line px-4 text-sm font-semibold text-fg2 lg:grid",
						)}
					>
						<span>Time</span>
						<span>Type</span>
						<span>Reason</span>
						<span>Message</span>
						<span className="text-right">Count</span>
						<span>Object</span>
					</div>
					{isLoading &&
						SKELETON_ROWS.map((i) => (
							<div key={i} className={ROW}>
								<Skeleton className="h-3.5 w-28" />
								<Skeleton className="h-4 w-12" />
								<Skeleton className={cn(WIDE, "h-3.5 w-20")} />
								<Skeleton className={cn(WIDE, "h-3.5 w-full")} />
								<Skeleton className={cn(COUNT, "h-3.5 w-6")} />
								<Skeleton className={cn(WIDE, "h-3.5 w-32")} />
							</div>
						))}
					{error !== null && (
						<div className="px-4 py-5 text-bad-fg">{getErrorMessage(error, "Failed to load events")}</div>
					)}
					{!isLoading &&
						slice.rows.map((e, i) => {
							const sev = severityBadge(severityOf(e));
							const ms = maybeTsMs(e.timestamp);
							const object = objectLabel(e);
							return (
								<div key={`${String(ms)}-${String(i)}`} className={ROW}>
									<span className="text-fg3 tabular-nums">{ms === undefined ? "—" : formatFullTime(ms)}</span>
									<Badge tone={sev.tone} size="sm" className="justify-self-start">
										{sev.label}
									</Badge>
									<span className={cn(WIDE, "truncate font-medium")} title={e.reason}>
										{e.reason}
									</span>
									<span className={cn(WIDE, "wrap-break-word text-fg2 lg:truncate")} title={e.message}>
										{e.message}
									</span>
									<span className={cn(COUNT, "text-fg3 tabular-nums")}>
										{e.count.toString()}
										<span className="lg:hidden">{e.count === 1n ? " time" : " times"}</span>
									</span>
									<span className={cn(WIDE, "truncate text-fg3")} title={object}>
										{object}
									</span>
								</div>
							);
						})}
					{!isLoading && error === null && rows.length === 0 && (
						<EmptyState
							icon={hasFilters ? <SearchXIcon /> : <ActivityIcon />}
							title={hasFilters ? "No events match" : `No events for ${resourceName}`}
							query={query.trim()}
							action={
								hasFilters ? (
									<Button variant="outline" onClick={clearFilters}>
										<XIcon />
										Clear filters
									</Button>
								) : undefined
							}
						/>
					)}
				</div>
			</div>
			<Pager
				label={rows.length > 0 ? pageRangeLabel(slice.page, size, rows.length) : "0 of 0"}
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
