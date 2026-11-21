import { useListNavigation } from "@/hooks/useListNavigation";
import { useState } from "react";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { Link } from "react-router";
import { PlusIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { ErrorState } from "@/components/design/ErrorState";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Pager } from "@/components/design/Pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceEvents } from "@/hooks/useWorkspaceEvents";
import { formatCount } from "@/lib/format";
import { pagedRows, pagingParsers, type Paging } from "@/lib/paging";
import { resolveWindow, type TimeWindow } from "@/lib/obs-time";
import { newResourcePath } from "@/lib/routes";
import { agoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { filteredResources, useObs } from "./context";
import { EventDetail, EventIcon } from "./EventDetail";
import { eventValue, eventItem, eventWindow, type EventItem, type Severity } from "./events";
import { ObsHero, WidenRangeButton } from "./ObsEmpty";
import { liveTokens, passes, queryStr, type FieldKey, type Token } from "./query";
import { QueryBar } from "./QueryBar";
import { PAGE_SIZES } from "./shared";
import { LiveButton } from "./Toolbar";
import { ViewHeader } from "./ViewHeader";

const DEFAULT_PAGE_SIZE = 25;
const EVENTS_PARAMS = {
	...pagingParsers(PAGE_SIZES, DEFAULT_PAGE_SIZE),
	live: parseAsStringLiteral(["0"]),
	event: parseAsString,
};
const NOW_TICK_MS = 30_000;
const HISTORY_BUCKET_MS = 15 * 60_000;

/** Window start rounded down to a bucket, so the history query key holds still between ticks. */
function historyStart(range: TimeWindow, nowMs: number): number {
	return Math.floor(resolveWindow(range, nowMs).from / HISTORY_BUCKET_MS) * HISTORY_BUCKET_MS;
}

type Show = "all" | "warning" | "error";

const SHOW_TOKENS: Record<Exclude<Show, "all">, Token> = {
	warning: { neg: false, key: "severity", value: "warning" },
	error: { neg: false, key: "severity", value: "error" },
};

export function EventsView() {
	const { orgId, workspaceId, resources, resourcesLoading, range, tokens, text, setQuery } = useObs();
	const [params, setParams] = useQueryStates(EVENTS_PARAMS, { history: "replace" });
	const live = params.live === null;
	const [openedAt] = useState(Date.now);
	const { active: environment } = useEnvironments();
	const now = useNow(NOW_TICK_MS);
	const nowMs = now.getTime();

	const { events, isLoading, error, refetch } = useWorkspaceEvents(
		workspaceId,
		resources.map((r) => r.raw),
		live,
		historyStart(range, nowMs),
	);
	const { from, to } = eventWindow(range, nowMs, tsMs(events[0]?.timestamp));
	const visible = new Set(filteredResources("events", resources, tokens).map((r) => r.id));
	const words = text.toLowerCase().split(" ").filter(Boolean);
	const inRange = events
		.filter((e) => {
			const ts = tsMs(e.timestamp);
			return ts >= from && ts <= to;
		})
		.map(eventItem);
	const all = inRange.filter((e) => visible.has(e.resourceId));
	const sevCounts: Record<Severity, number> = { error: 0, warning: 0, normal: 0 };
	for (const e of all) sevCounts[e.severity]++;
	const keys = ["type", "severity", "region", "reason", "actor"] as const;
	const items = all.filter(
		(e) =>
			keys.every((key) => passes([eventValue(e, key)], liveTokens("events", tokens, key))) &&
			words.every((w) => `${e.type} ${e.title} ${e.reason} ${e.detail} ${e.resourceName}`.toLowerCase().includes(w)),
	);
	const show: Show = tokens.some((t) => t.key === "severity" && !t.neg && t.value === "error")
		? "error"
		: tokens.some((t) => t.key === "severity" && !t.neg && t.value === "warning")
			? "warning"
			: "all";

	const paging = { page: params.page, size: params.size };
	const setPaging = (next: Paging) => {
		void setParams(next);
	};
	const paged = pagedRows(items, paging, setPaging, PAGE_SIZES, "0");
	const selKey = params.event;
	const selIdx = selKey !== null ? items.findIndex((x) => x.id === selKey) : -1;
	const selItem = selIdx >= 0 ? items[selIdx] : undefined;
	const select = (key: string | null) => {
		const idx = key === null ? -1 : items.findIndex((x) => x.id === key);
		void setParams(idx >= 0 ? { event: key, page: Math.floor(idx / paging.size) } : { event: key });
	};
	const loading = isLoading || resourcesLoading;
	const query = queryStr(tokens, text);
	const valueCounts = (key: FieldKey): [string, number][] => {
		const counts = new Map<string, number>();
		for (const e of inRange) {
			const value = eventValue(e, key);
			if (value !== "") counts.set(value, (counts.get(value) ?? 0) + 1);
		}
		return [...counts];
	};
	const onListKeyDown = useListNavigation(
		paged.rows.map((e) => e.id),
		selItem?.id ?? null,
		select,
	);
	const table = useDataTable("events", {
		columns: eventColumns(nowMs),
		data: paged.rows,
		getRowId: (e) => e.id,
	});

	const header = (
		<ViewHeader
			actions={
				<LiveButton
					on={live}
					title={live ? "Pause event stream" : "Stream new events"}
					onToggle={() => {
						void setParams({ live: live ? "0" : null });
					}}
				/>
			}
		/>
	);

	if (!loading && error !== null) {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ErrorState error={error} what="events" rpc={listEvents} onRetry={refetch} />
			</>
		);
	}
	if (!loading && inRange.length === 0 && query === "") {
		const hasResources = resources.length > 0;
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ObsHero
					view="events"
					title={hasResources ? "No events in this range" : "No events yet"}
					body={`Deploys, restarts, failed health checks and scaling${environment === undefined ? "" : ` in ${environment.name}`} show up here as they happen.`}
					action={
						hasResources ? (
							<WidenRangeButton />
						) : (
							<Button
								nativeButton={false}
								render={<Link to={newResourcePath(orgId, workspaceId, environment ? { env: environment.slug } : {})} />}
							>
								<PlusIcon />
								New resource
							</Button>
						)
					}
				/>
			</>
		);
	}

	return (
		<>
			{header}
			<QueryBar valueCounts={valueCounts} />
			<div>
				<section aria-label="Events" className="flex min-w-0 flex-col rounded-card bg-panel">
					<div className="flex flex-wrap items-center gap-2.5 px-5 pt-3.5 pb-2.5">
						<ToggleGroup
							variant="segmented"
							aria-label="Show"
							value={[show]}
							onValueChange={(v: string[]) => {
								const rest = tokens.filter((t) => t.key !== "severity");
								const next = v[0];
								setQuery(next === "warning" || next === "error" ? [...rest, SHOW_TOKENS[next]] : rest, text);
							}}
						>
							<ToggleGroupItem value="all" count={formatCount(all.length)}>
								All
							</ToggleGroupItem>
							<ToggleGroupItem value="warning" count={formatCount(sevCounts.warning)}>
								Warnings
							</ToggleGroupItem>
							<ToggleGroupItem value="error" count={formatCount(sevCounts.error)}>
								Errors
							</ToggleGroupItem>
						</ToggleGroup>
					</div>
					<DataTable
						table={table}
						label="Events"
						loading={loading}
						empty={<div className="px-6 py-7 text-center text-[13px] text-fg3">No events match.</div>}
						activeRowId={selItem?.id ?? null}
						onRowClick={(row) => {
							select(row.id);
						}}
						rowLabel={(row) => row.original.title}
						rowClassName={(row) => row.original.ts > openedAt && "animate-row-flash motion-reduce:animate-none"}
					/>
					<Pager {...paged.pager} {...paged.ends} />
				</section>
				<EventDetail
					onKeyDown={onListKeyDown}
					item={selItem ?? null}
					onPrev={
						selIdx > 0
							? () => {
									select(items[selIdx - 1]?.id ?? null);
								}
							: null
					}
					onNext={
						selIdx < items.length - 1
							? () => {
									select(items[selIdx + 1]?.id ?? null);
								}
							: null
					}
					onClose={() => {
						select(null);
					}}
				/>
			</div>
		</>
	);
}

function eventColumns(nowMs: number): DataColumn<EventItem>[] {
	return [
		{
			id: "severity",
			size: 40,
			minSize: 40,
			enableResizing: false,
			meta: { label: "Severity", fit: true },
			cell: ({ row: { original: e } }) => (
				<>
					<EventIcon item={e} />
					<span className="sr-only">{e.severity}</span>
				</>
			),
		},
		{
			id: "event",
			header: "Event",
			size: 320,
			meta: { grow: true, primary: true },
			cell: ({ row: { original: e } }) => (
				<span className="flex min-w-0 flex-col gap-0.5">
					<CellText text={e.title} className={cn("text-[13.5px]", e.severity === "error" && "text-bad-fg")} />
					<CellText text={e.detail} className="font-mono text-[11.5px] text-fg3" />
				</span>
			),
		},
		{
			id: "resource",
			header: "Resource",
			size: 160,
			meta: { fit: true, fitMax: 280 },
			cell: ({ row }) => <CellText text={row.original.resourceName} className="text-fg2" />,
		},
		{
			id: "time",
			header: "Time",
			size: 110,
			meta: { align: "end" },
			cell: ({ row }) => (
				<span className="truncate font-mono text-[12px] whitespace-nowrap text-fg3">
					{agoLabel(row.original.ts, nowMs)}
				</span>
			),
		},
	];
}
