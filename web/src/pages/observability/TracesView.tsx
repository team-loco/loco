import { useListNavigation } from "@/hooks/useListNavigation";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { listTraces } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { TraceOrder, TraceStatus, type TraceSummary } from "@gen/loco/observability/v1/observability_pb";
import { useCallback, useState, type ReactNode } from "react";
import { functionalUpdate } from "@tanstack/react-table";
import { RefreshCwIcon, ZoomOutIcon } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";

import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useGetTrace } from "@/hooks/useGetTrace";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { scopeReady as canRun } from "@/hooks/observabilityScope";
import { useTokenPage } from "@/hooks/useTokenPage";
import { useTraceBuckets } from "@/hooks/useTraceBuckets";
import { isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { resolveWindow, WINDOW_PARSERS, windowValues, type TimeWindow } from "@/lib/obs-time";
import { pageTokensParser, pagingParsers, tokenPageLabel, tokenPager } from "@/lib/paging";
import { formatClock, formatIsoDate, msToTimestamp } from "@/lib/time";
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
import { traceIdentity } from "./trace-service";
import { ViewHeader } from "./ViewHeader";
import { TRACE_ID_PATTERN, durationLabel, spansMayArrive, summarizeTraces, timestampMs, uniqueSpans } from "./traces";

const SORT_IDS = ["duration", "operation", "resource", "status", "spans"] as const;
const DEFAULT_PAGE_SIZE = 25;
// Page tokens sit in the obs view's page parameter, which query and range changes clear.
const TRACES_PARAMS = {
	page: pageTokensParser,
	size: pagingParsers(PAGE_SIZES, DEFAULT_PAGE_SIZE).size,
	...WINDOW_PARSERS,
	...QUERY_PARSERS,
	sort: parseAsStringLiteral(SORT_IDS),
	dir: parseAsStringLiteral(["asc"]),
	trace: parseAsString.withDefault(""),
	span: parseAsString,
	widths: widthsParser,
};
const ERROR_TOKEN = { neg: false, key: "status" as const, value: "error" };
const ARRIVAL_POLL_MS = 5000;

type SortId = (typeof SORT_IDS)[number];
const START_TIME = "when";
const TRACE_ORDERS: Record<SortId, TraceOrder> = {
	duration: TraceOrder.DURATION,
	operation: TraceOrder.OPERATION,
	resource: TraceOrder.RESOURCE,
	status: TraceOrder.STATUS,
	spans: TraceOrder.SPAN_COUNT,
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
	const {
		workspaceId,
		installation,
		environmentId,
		environmentLoading,
		resources,
		resourcesLoading,
		scopedResourceIds,
		resourceById,
		range,
		tokens,
		text,
		setQuery,
	} = useObs();
	const [params, setParams] = useQueryStates(TRACES_PARAMS);
	const resetPage = useCallback(() => {
		void setParams({ page: null }, { history: "replace" });
	}, [setParams]);
	const [frozenEnd, setFrozenEnd] = useState(Date.now);
	const bounds = resolveWindow(range, frozenEnd);
	const scopeReady = scopedResourceIds === null || scopedResourceIds.length > 0;
	const runnable = canRun({ workspaceId, environmentId, installation });
	const orderBy = params.sort === null ? TraceOrder.START_TIME : TRACE_ORDERS[params.sort];
	const ascending = params.dir === "asc";
	const query = queryStr(tokens, text);
	const filters = {
		workspaceId,
		installation,
		environmentId,
		resourceIds: scopedResourceIds ?? [],
		start: bounds.from,
		end: bounds.to,
	};
	const request = {
		scope: { workspaceId, installation, environmentId, resourceIds: filters.resourceIds },
		startTime: msToTimestamp(bounds.from),
		endTime: msToTimestamp(bounds.to),
		query,
	};
	const listed = useTokenPage(
		listTraces,
		{ ...request, pageSize: params.size, orderBy, descending: !ascending },
		params.page,
		{ enabled: scopeReady && runnable, staleTime: Infinity },
		null,
		resetPage,
	);
	const browse = listed.query;
	const widthSeconds = bucketSeconds(bounds.to - bounds.from);
	const overview = useTraceBuckets({
		...filters,
		query,
		intervalSeconds: widthSeconds,
		enabled: scopeReady,
	});
	const unfiltered = tokens.filter((t) => t.key !== "status");
	const statusFree = unfiltered.length === tokens.length;
	// The tabs count from the histogram without status tokens, which the All tab's overview has already loaded.
	const unfilteredOverview = useTraceBuckets({
		...filters,
		query: queryStr(unfiltered, text),
		intervalSeconds: widthSeconds,
		enabled: scopeReady && !statusFree,
	});
	const tabTotals = scopeReady ? (statusFree ? overview : unfilteredOverview).data?.total : undefined;
	const openId = params.trace;
	const shownId = useHeldWhileClosed(openId, openId !== "");
	const detail = useGetTrace({
		workspaceId,
		installation,
		environmentId,
		resourceIds: scopedResourceIds ?? [],
		traceId: shownId,
		enabled: openId !== "" && TRACE_ID_PATTERN.test(openId) && scopeReady,
		refetchInterval: ({ state }) =>
			spansMayArrive(state.data?.spans ?? [], state.dataUpdatedAt) ? ARRIVAL_POLL_MS : false,
	});

	const traces = scopeReady ? (browse.data?.summaries ?? []) : [];
	const traceCount = overview.data?.total?.traceCount;
	const total = scopeReady && traceCount !== undefined ? Number(traceCount) : undefined;
	const countLabel = (count: bigint | undefined) => (count === undefined ? undefined : formatCount(Number(count)));
	const errorsOnly = tokens.some((t) => sameToken(t, ERROR_TOKEN));
	const pager = tokenPager(params.page, listed.nextPageToken, (page) => {
		void setParams({ page }, { history: "replace" });
	});
	const listLoading = browse.isLoading || listed.resetting;
	const loading = environmentLoading || listLoading || (!scopeReady && resourcesLoading);
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
		if (key === "component") return [...new Set(traces.map((t) => t.serviceName))].map((name) => [name, 0]);
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
					className="size-8"
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

	if (!loading && listed.error !== null && isUnimplemented(listed.error)) {
		return (
			<>
				{header}
				<ObsHero
					view="traces"
					title="Traces unavailable"
					body={
						installation
							? "This installation has no trace store connected. Loco's own traces show up here once it does."
							: "This installation has no trace store connected. Traces your services send over OTLP show up here once it does."
					}
				/>
			</>
		);
	}
	const rejected = listed.error === null ? null : queryError(listed.error, query);
	if (!loading && listed.error !== null && rejected === null) {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ErrorState error={listed.error} what="traces" rpc={listTraces} onRetry={browse.refetch} />
			</>
		);
	}
	// An unfiltered first page with no rows means the range holds no traces at all.
	const unfilteredFirstPage = pager.page === 0 && query === "";
	if (unfilteredFirstPage && browse.data?.summaries.length === 0) {
		return (
			<>
				{header}
				<ObsHero view="traces" title="No traces collected" action={<WidenRangeButton />}>
					{!installation && <TraceSetup />}
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
						<ToggleGroupItem value="all" count={countLabel(tabTotals?.traceCount)}>
							All
						</ToggleGroupItem>
						<ToggleGroupItem value="errors" count={countLabel(tabTotals?.errorCount)}>
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
					resourceName={(t) => traceIdentity(t, resourceById.get(t.resourceId)?.slug)}
					resourceHeader={installation ? "Component" : "Resource"}
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
					label={tokenPageLabel(pager.page, params.size, traces.length, total)}
					page={pager.page}
					loading={loading}
					total={traces.length}
					pageSizes={PAGE_SIZES}
					pageSize={params.size}
					onPageSize={(size) => {
						void setParams({ page: null, size }, { history: "replace" });
					}}
					onFirst={loading ? null : pager.onFirst}
					onPrev={loading ? null : pager.onPrev}
					onNext={loading ? null : pager.onNext}
				/>
			</section>
			<TraceDrawer
				onKeyDown={onListKeyDown}
				open={openId !== ""}
				traceId={shownId}
				trace={openTrace}
				arriving={spansMayArrive(detailSpans, detail.dataUpdatedAt)}
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
	resourceHeader,
}: {
	crossesDay: boolean;
	today: string;
	resourceName: (t: TraceSummary) => ReturnType<typeof traceIdentity>;
	resourceHeader: string;
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
			header: resourceHeader,
			size: 110,
			minSize: 100,
			maxSize: 640,
			sortDescFirst: true,
			cell: ({ row }) => {
				const { name, service } = resourceName(row.original);
				return <CellText text={name} detail={service} className="text-[12.5px]" />;
			},
		},
		{
			id: "span",
			accessorFn: (t) => t.rootSpanName,
			header: "Root span",
			size: 260,
			sortDescFirst: true,
			meta: { primary: true },
			cell: ({ row }) => {
				const { rootSpanName, httpPath } = row.original;
				const path = httpPath !== "" && !rootSpanName.includes(httpPath) ? httpPath : undefined;
				return <CellText text={rootSpanName || "Unnamed span"} detail={path} className="font-mono text-foreground" />;
			},
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
	resourceHeader,
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
	resourceName: (t: TraceSummary) => ReturnType<typeof traceIdentity>;
	resourceHeader: string;
	onOpen: (id: string) => void;
	sorting: { id: string; desc: boolean }[];
	onSorting: (next: { id: string; desc: boolean }) => void;
	widths: ColumnWidths;
	onWidths: (next: ColumnWidths) => void;
	empty: ReactNode;
}) {
	const columns = traceColumns({ crossesDay, today, resourceName, resourceHeader });
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
			band
			className="text-[12px] [--row-h:34px] [&_td]:py-1"
			loading={loading}
			tail
			separators
			empty={empty}
			activeRowId={openId}
			onRowClick={(row) => {
				onOpen(row.id);
			}}
			rowLabel={(row) => `Open trace ${row.original.rootSpanName || row.original.traceId}`}
		/>
	);
}
