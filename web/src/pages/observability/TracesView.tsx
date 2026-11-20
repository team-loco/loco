import { useListNavigation } from "@/hooks/useListNavigation";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { listTraces } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { TraceStatus, TraceSummarySort, type TraceSummary } from "@gen/loco/observability/v1/observability_pb";
import { useState, type ReactNode } from "react";
import { functionalUpdate } from "@tanstack/react-table";
import { RefreshCwIcon, ZoomOutIcon } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";

import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useGetTrace } from "@/hooks/useGetTrace";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { useListTraces } from "@/hooks/useListTraces";
import { useTraceBuckets } from "@/hooks/useTraceBuckets";
import { isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { resolveWindow, WINDOW_PARSERS, windowValues, type TimeWindow } from "@/lib/obs-time";
import { pageRangeLabel, pagingParsers, type Paging } from "@/lib/paging";
import { formatClock, formatIsoDate } from "@/lib/time";
import { cn } from "@/lib/utils";

import { widthsParser, type ColumnWidths } from "./columns";
import { useObs } from "./context";
import { fmtTs } from "./format";
import { NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import { QUERY_PARSERS, queryError, queryStr, sameToken, type FieldKey } from "./query";
import { QueryBar, QueryErrorLine } from "./QueryBar";
import { PAGE_SIZES } from "./shared";
import { TraceDrawer } from "./TraceDrawer";
import { bucketSeconds } from "./trace-overview";
import { TraceOverview } from "./TraceOverview";
import { TraceSetup } from "./TraceSetup";
import { traceServiceLabel } from "./trace-service";
import { ViewHeader } from "./ViewHeader";
import { TRACE_ID_PATTERN, durationLabel, summarizeTraces, timestampMs, uniqueSpans } from "./traces";

const SORT_IDS = ["duration", "operation", "resource", "status", "spans"] as const;
const DEFAULT_PAGE_SIZE = 25;
const TRACES_PARAMS = {
	...pagingParsers(PAGE_SIZES, DEFAULT_PAGE_SIZE),
	...WINDOW_PARSERS,
	...QUERY_PARSERS,
	sort: parseAsStringLiteral(SORT_IDS),
	dir: parseAsStringLiteral(["asc"]),
	trace: parseAsString.withDefault(""),
	span: parseAsString,
	widths: widthsParser,
};
const ERROR_TOKEN = { neg: false, key: "status" as const, value: "error" };
const COUNT_LIMIT = 1;

type SortId = (typeof SORT_IDS)[number];
const START_TIME = "when";
const TRACE_SORTS: Record<SortId, TraceSummarySort> = {
	duration: TraceSummarySort.DURATION,
	operation: TraceSummarySort.OPERATION,
	resource: TraceSummarySort.RESOURCE,
	status: TraceSummarySort.STATUS,
	spans: TraceSummarySort.SPAN_COUNT,
};

export function TracesView() {
	const { range } = useObs();
	// Lives above the session, which remounts when a zoom changes the range.
	const [zoomPrev, setZoomPrev] = useState<TimeWindow | null>(null);
	return <TraceSession key={JSON.stringify(range)} zoomPrev={zoomPrev} setZoomPrev={setZoomPrev} />;
}

function TraceSession({
	zoomPrev,
	setZoomPrev,
}: {
	zoomPrev: TimeWindow | null;
	setZoomPrev: (zoom: TimeWindow | null) => void;
}) {
	const timePref = useTimePreferences();
	const { workspaceId, resources, resourcesLoading, scopedResourceIds, resourceById, range, tokens, text, setQuery } =
		useObs();
	const [params, setParams] = useQueryStates(TRACES_PARAMS);
	const setPaging = (next: Paging) => {
		void setParams(next, { history: "replace" });
	};
	const { active, isLoading: environmentLoading } = useEnvironments();
	const environmentId = active?.id ?? "";
	const [frozenEnd, setFrozenEnd] = useState(Date.now);
	const bounds = resolveWindow(range, frozenEnd);
	const scopeReady = scopedResourceIds === null || scopedResourceIds.length > 0;
	const paging = { page: params.page, size: params.size };
	const sort = params.sort === null ? TraceSummarySort.START_TIME : TRACE_SORTS[params.sort];
	const ascending = params.dir === "asc";
	const query = queryStr(tokens, text);
	const filters = {
		workspaceId,
		environmentId,
		resourceIds: scopedResourceIds ?? [],
		start: bounds.from,
		end: bounds.to,
	};
	const browse = useListTraces({
		...filters,
		query,
		limit: paging.size,
		offset: paging.page * paging.size,
		sort,
		ascending,
		enabled: scopeReady,
	});
	const widthSeconds = bucketSeconds(bounds.to - bounds.from);
	const overview = useTraceBuckets({
		...filters,
		query,
		intervalSeconds: widthSeconds,
		enabled: scopeReady,
	});
	const unfiltered = tokens.filter((t) => t.key !== "status");
	const allQuery = queryStr(unfiltered, text);
	const errorQuery = queryStr([...unfiltered, ERROR_TOKEN], text);
	const countOf = (countQuery: string) => ({
		...filters,
		query: countQuery,
		limit: COUNT_LIMIT,
		offset: 0,
		sort: TraceSummarySort.START_TIME,
		enabled: scopeReady && countQuery !== query,
	});
	const allCount = useListTraces(countOf(allQuery));
	const errorCount = useListTraces(countOf(errorQuery));
	const openId = params.trace;
	const shownId = useHeldWhileClosed(openId, openId !== "");
	const detail = useGetTrace({
		workspaceId,
		environmentId,
		resourceIds: scopedResourceIds ?? [],
		traceId: shownId,
		enabled: openId !== "" && TRACE_ID_PATTERN.test(openId) && scopeReady,
	});

	const traces = scopeReady ? (browse.data?.summaries ?? []) : [];
	const total = scopeReady ? Number(browse.data?.totalCount ?? 0n) : 0;
	const countLabel = (countQuery: string, data: { totalCount: bigint } | undefined) => {
		const counted = countQuery === query ? browse.data : data;
		return scopeReady && counted !== undefined ? formatCount(Number(counted.totalCount)) : undefined;
	};
	const errorsOnly = tokens.some((t) => sameToken(t, ERROR_TOKEN));
	const go = (page: number) => () => {
		setPaging({ page, size: paging.size });
	};
	const pages = Math.max(1, Math.ceil(total / paging.size));
	const loading = environmentLoading || browse.isLoading || (!scopeReady && resourcesLoading);
	const detailSpans = uniqueSpans(detail.data?.spans ?? []);
	const openTrace = summarizeTraces(detailSpans)[0];
	const open = (id: string) => {
		void setParams({ trace: id, span: null });
	};
	const onListKeyDown = useListNavigation(
		traces.map((trace) => trace.traceId),
		params.span === null ? openId || null : null,
		open,
	);
	const close = () => {
		void setParams({ trace: null, span: null });
	};
	const crossesDay = formatIsoDate(bounds.from, timePref) !== formatIsoDate(bounds.to, timePref);
	const valueCounts = (key: FieldKey): [string, number][] => {
		if (key === "resource") return resources.map((r) => [r.slug, 0]);
		if (key === "status")
			return [
				["error", 0],
				["ok", 0],
			];
		if (key === "trace") return traces.map((t) => [t.traceId, Number(t.spanCount)]);
		if (key === "duration")
			return [
				[">100", 0],
				[">500", 0],
				[">1000", 0],
			];
		return [];
	};

	const zoom = (from: number, to: number) => {
		setZoomPrev(zoomPrev ?? range);
		void setParams({
			...windowValues({ kind: "absolute", from, to }),
			page: null,
		});
	};
	const resetZoom = () => {
		if (zoomPrev === null) return;
		setZoomPrev(null);
		void setParams({ ...windowValues(zoomPrev), page: null });
	};
	const sorting = [{ id: params.sort === "operation" ? "span" : (params.sort ?? START_TIME), desc: !ascending }];
	const onSorting = (next: { id: string; desc: boolean }) => {
		const id = next.id === "span" ? "operation" : next.id;
		void setParams({
			sort: SORT_IDS.find((sortId) => sortId === id) ?? null,
			dir: next.desc ? null : "asc",
			page: null,
		});
	};

	const header = (
		<ViewHeader
			actions={
				<Button
					variant="outline"
					size="icon"
					aria-label="Refresh"
					title="Refresh"
					disabled={browse.isFetching}
					onClick={() => {
						setFrozenEnd(Date.now());
						void setParams({ page: null }, { history: "replace" });
						if (detail.isEnabled) void detail.refetch();
					}}
				>
					<RefreshCwIcon className={cn(browse.isFetching && "animate-spin motion-reduce:animate-none")} />
				</Button>
			}
		/>
	);

	if (!loading && browse.error !== null && isUnimplemented(browse.error)) {
		return (
			<>
				{header}
				<ObsHero
					view="traces"
					title="Traces aren't available here yet"
					body="This installation has no trace store connected. Traces your services send over OTLP show up here once it does."
				/>
			</>
		);
	}
	const rejected = browse.error === null ? null : queryError(browse.error, query);
	if (!loading && browse.error !== null && rejected === null) {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ErrorState error={browse.error} what="traces" rpc={listTraces} onRetry={browse.refetch} />
			</>
		);
	}
	if (!loading && total === 0 && query === "") {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ObsHero
					view="traces"
					title="Send your first trace"
					body="Instrument your app to send traces. Loco configures the collector connection by default; you can also configure your SDK directly."
					action={<WidenRangeButton />}
				>
					<TraceSetup />
				</ObsHero>
			</>
		);
	}

	return (
		<>
			{header}
			<QueryBar valueCounts={valueCounts} />
			{rejected !== null && <QueryErrorLine error={rejected} />}
			<Section
				title="Overview"
				card={false}
				actions={
					zoomPrev !== null && (
						<Button variant="ghost" size="sm" onClick={resetZoom}>
							<ZoomOutIcon />
							Reset zoom
						</Button>
					)
				}
			>
				<TraceOverview
					data={scopeReady ? overview.data : undefined}
					loading={scopeReady && overview.isLoading}
					error={rejected === null ? overview.error : null}
					widthSeconds={widthSeconds}
					from={bounds.from}
					to={bounds.to}
					onZoom={zoom}
				/>
			</Section>
			<section aria-label="Trace list" className="flex min-w-0 flex-col rounded-card bg-panel">
				<div className="flex flex-wrap items-center gap-2 px-5 pt-3 pb-1.5">
					<ToggleGroup
						variant="segmented"
						aria-label="Show"
						value={[errorsOnly ? "errors" : "all"]}
						onValueChange={(v: string[]) => {
							setQuery(v[0] === "errors" ? [...unfiltered, ERROR_TOKEN] : unfiltered, text);
						}}
					>
						<ToggleGroupItem value="all" count={countLabel(allQuery, allCount.data)}>
							All
						</ToggleGroupItem>
						<ToggleGroupItem value="errors" count={countLabel(errorQuery, errorCount.data)}>
							Errors
						</ToggleGroupItem>
					</ToggleGroup>
				</div>
				<TraceList
					traces={traces}
					loading={loading}
					openId={openId}
					crossesDay={crossesDay}
					today={formatIsoDate(frozenEnd, timePref)}
					resourceName={(t) => traceServiceLabel(t, resourceById.get(t.resourceId)?.slug)}
					onOpen={open}
					sorting={sorting}
					onSorting={onSorting}
					widths={params.widths}
					onWidths={(next) => {
						void setParams({ widths: next }, { history: "replace" });
					}}
					empty={rejected === null ? <NoMatches title="No requests match." /> : null}
				/>
				<Pager
					label={traces.length > 0 ? pageRangeLabel(paging.page, paging.size, total) : "0"}
					page={paging.page}
					pages={pages}
					total={total}
					pageSizes={PAGE_SIZES}
					pageSize={paging.size}
					onPageSize={(size) => {
						setPaging({ page: 0, size });
					}}
					onFirst={!loading && paging.page > 0 ? go(0) : null}
					onPrev={!loading && paging.page > 0 ? go(paging.page - 1) : null}
					onNext={!loading && paging.page < pages - 1 ? go(paging.page + 1) : null}
					onLast={!loading && paging.page < pages - 1 ? go(pages - 1) : null}
				/>
			</section>
			<TraceDrawer
				onKeyDown={onListKeyDown}
				open={openId !== ""}
				traceId={shownId}
				trace={openTrace}
				loading={detail.isLoading}
				error={detail.error}
				onRetry={async () => await detail.refetch()}
				onClose={close}
			/>
		</>
	);
}

function TraceTime({ trace, crossesDay, today }: { trace: TraceSummary; crossesDay: boolean; today: string }) {
	const timePref = useTimePreferences();
	const startMs = timestampMs(trace.startTime);
	return (
		<span className="truncate font-mono whitespace-nowrap text-fg3">
			{crossesDay || formatIsoDate(startMs, timePref) !== today
				? fmtTs(startMs, false, timePref)
				: formatClock(startMs, false, timePref)}
		</span>
	);
}

function traceColumns({
	crossesDay,
	today,
	resourceName,
}: {
	crossesDay: boolean;
	today: string;
	resourceName: (t: TraceSummary) => string;
}): DataColumn<TraceSummary>[] {
	return [
		{
			id: START_TIME,
			accessorFn: (t) => timestampMs(t.startTime),
			header: "Time",
			size: 150,
			sortDescFirst: true,
			cell: ({ row }) => <TraceTime trace={row.original} crossesDay={crossesDay} today={today} />,
		},
		{
			id: "resource",
			accessorFn: (t) => t.resourceId,
			header: "Resource",
			size: 150,
			minSize: 100,
			maxSize: 640,
			sortDescFirst: true,
			meta: { fit: true, fitMax: 240 },
			cell: ({ row }) => <CellText text={resourceName(row.original)} className="text-[12.5px]" />,
		},
		{
			id: "span",
			accessorFn: (t) => t.rootSpanName,
			header: "Root span",
			size: 260,
			sortDescFirst: true,
			meta: { grow: true, primary: true },
			cell: ({ row }) => (
				<CellText text={row.original.rootSpanName || "Unnamed span"} className="font-mono text-foreground" />
			),
		},
		{
			id: "status",
			accessorFn: (t) => t.status,
			header: "Status",
			size: 80,
			sortDescFirst: true,
			cell: ({ row }) => {
				const failed = row.original.status === TraceStatus.ERROR;
				return <span className={cn("font-mono", failed ? "text-bad-fg" : "text-fg3")}>{failed ? "Error" : "OK"}</span>;
			},
		},
		{
			id: "duration",
			accessorFn: (t) => Number(t.durationNanos),
			header: "Duration",
			size: 100,
			sortDescFirst: true,
			meta: { align: "end" },
			cell: ({ row }) => (
				<span
					className={cn("font-mono", row.original.status === TraceStatus.ERROR ? "text-bad-fg" : "text-foreground")}
				>
					{durationLabel(row.original.durationNanos)}
				</span>
			),
		},
		{
			id: "spans",
			accessorFn: (t) => Number(t.spanCount),
			header: "Spans",
			size: 70,
			sortDescFirst: true,
			meta: { align: "end" },
			cell: ({ row }) => <span className="font-mono text-fg3">{String(row.original.spanCount)}</span>,
		},
	];
}

function TraceList({
	traces,
	loading,
	openId,
	crossesDay,
	today,
	resourceName,
	onOpen,
	sorting,
	onSorting,
	widths,
	onWidths,
	empty,
}: {
	traces: TraceSummary[];
	loading: boolean;
	openId: string;
	crossesDay: boolean;
	today: string;
	resourceName: (t: TraceSummary) => string;
	onOpen: (id: string) => void;
	sorting: { id: string; desc: boolean }[];
	onSorting: (next: { id: string; desc: boolean }) => void;
	widths: ColumnWidths;
	onWidths: (next: ColumnWidths) => void;
	empty: ReactNode;
}) {
	const columns = traceColumns({ crossesDay, today, resourceName });
	const table = useDataTable("traces", {
		columns,
		data: traces,
		getRowId: (t) => t.traceId,
		manualSorting: true,
		state: { sorting, columnSizing: widths },
		onColumnSizingChange: (updater) => {
			onWidths(functionalUpdate(updater, widths));
		},
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) onSorting(next);
		},
	});
	return (
		<DataTable
			table={table}
			label="Traces"
			className="text-[12px] [--row-h:34px] [&_td]:py-1"
			loading={loading}
			empty={empty}
			activeRowId={openId}
			onRowClick={(row) => {
				onOpen(row.id);
			}}
			rowLabel={(row) => `Open trace ${row.original.rootSpanName || row.original.traceId}`}
		/>
	);
}
