import { TraceStatus, TraceSummarySort, type TraceSummary } from "@gen/loco/observability/v1/observability_pb";
import { useState } from "react";
import { RefreshCwIcon, ZoomOutIcon } from "lucide-react";
import { Navigate } from "react-router";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useGetTrace } from "@/hooks/useGetTrace";
import { useListTraces } from "@/hooks/useListTraces";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { resolveWindow, writeWindow, type TimeWindow } from "@/lib/obs-time";
import { pageRangeLabel } from "@/lib/paging";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";

import { readWidths, writeWidths, type ColumnKey, type ColumnWidths } from "./columns";
import { filteredResources, useObs } from "./context";
import { fmtTs } from "./format";
import { LIST_HEAD_CLASS, listRowClass, useResizableColumns } from "./ListGrid";
import { NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import {
	durationBounds,
	liveTokens,
	passes,
	queryStr,
	sameToken,
	writeQuery,
	type FieldKey,
	type Token,
} from "./query";
import { QueryBar } from "./QueryBar";
import { PAGE_SIZES, readPaging } from "./shared";
import { TraceDrawer } from "./TraceDrawer";
import { TraceScatter, type ScatterZoom } from "./TraceScatter";
import { TraceSetup } from "./TraceSetup";
import { traceServiceLabel } from "./trace-service";
import { ViewHeader } from "./ViewHeader";
import { TRACE_ID_PATTERN, durationLabel, summarizeTraces, timestampMs, uniqueSpans } from "./traces";

const DEFAULT_PAGE_SIZE = 25;
const ERROR_TOKEN = { neg: false, key: "status" as const, value: "error" };
// The chart plots the newest traces up to this many; the API caps a page at 10,000.
const SCATTER_LIMIT = 2000;
const COUNT_LIMIT = 1;
const DURATION_DIGITS = 3;

const TRACE_COLUMNS: { key: ColumnKey; label: string; sort?: TraceSummarySort; right?: boolean; title?: string }[] = [
	{ key: "when", label: "When", sort: TraceSummarySort.START_TIME },
	{ key: "resource", label: "Resource" },
	{ key: "span", label: "Root span" },
	{ key: "status", label: "Status" },
	{
		key: "duration",
		label: "Duration",
		sort: TraceSummarySort.DURATION,
		right: true,
		title: "From the first span's start to the last span's end",
	},
	{ key: "spans", label: "Spans", right: true },
];
const TRACE_WIDTHS: Record<ColumnKey, number> = {
	when: 150,
	resource: 110,
	span: 260,
	status: 80,
	duration: 100,
	spans: 70,
};
const SKELETON_ROWS = 8;

function traceColumnWidth(key: ColumnKey, widths: ColumnWidths): number {
	return widths[key] ?? TRACE_WIDTHS[key] ?? 0;
}

interface ZoomState {
	range: TimeWindow;
	durations: Token[];
}

export function TracesView() {
	const { range } = useObs();
	// Lives above the session, which remounts when a zoom changes the range.
	const [zoomPrev, setZoomPrev] = useState<ZoomState | null>(null);
	return <TraceSession key={JSON.stringify(range)} zoomPrev={zoomPrev} setZoomPrev={setZoomPrev} />;
}

function durationToken(op: ">=" | "<=", ms: number): Token {
	return { neg: false, key: "duration", value: `${op}${String(Number(ms.toPrecision(DURATION_DIGITS)))}` };
}

function TraceSession({
	zoomPrev,
	setZoomPrev,
}: {
	zoomPrev: ZoomState | null;
	setZoomPrev: (zoom: ZoomState | null) => void;
}) {
	const {
		workspaceId,
		resources,
		resourcesLoading,
		scopedResourceIds,
		resourceById,
		range,
		tokens,
		text,
		setQuery,
		params,
		setParam,
		setParams,
		setPaging,
	} = useObs();
	const { active, isLoading: environmentLoading } = useEnvironments();
	const environmentId = active?.id ?? "";
	const [frozenEnd, setFrozenEnd] = useState(Date.now);
	const bounds = resolveWindow(range, frozenEnd);
	const traceToken = liveTokens("traces", tokens, "trace")[0];
	const lookup = traceToken?.value.toLowerCase() ?? "";
	const validLookup = lookup === "" || TRACE_ID_PATTERN.test(lookup);
	const resourceFilter = liveTokens("traces", tokens, "resource").length > 0;
	const visible = filteredResources("traces", resources, tokens);
	const filterReady = !resourceFilter || (!resourcesLoading && visible.length > 0);
	const scopeReady = scopedResourceIds === null || scopedResourceIds.length > 0;
	const paging = readPaging(params, DEFAULT_PAGE_SIZE);
	const statusTokens = liveTokens("traces", tokens, "status");
	const statuses = ["error", "ok"].filter((status) => passes([status], statusTokens));
	const statusReady = statuses.length > 0;
	const duration = durationBounds(tokens);
	const sort = params.get("sort") === "duration" ? TraceSummarySort.DURATION : TraceSummarySort.START_TIME;
	const ascending = params.get("dir") === "asc";
	const ready = filterReady && scopeReady && validLookup && duration !== null;
	const filters = {
		workspaceId,
		environmentId,
		resourceIds: resourceFilter ? visible.map((r) => r.id) : (scopedResourceIds ?? []),
		traceId: validLookup ? lookup : "",
		start: bounds.from,
		end: bounds.to,
		rootNameTerms: text.split(" ").filter(Boolean),
		...(duration?.minNanos !== undefined && { minDurationNanos: duration.minNanos }),
		...(duration?.maxNanos !== undefined && { maxDurationNanos: duration.maxNanos }),
	};
	const status =
		statuses.length === 1 ? (statuses[0] === "error" ? TraceStatus.ERROR : TraceStatus.OK) : TraceStatus.UNSPECIFIED;
	const browse = useListTraces({
		...filters,
		limit: paging.size,
		offset: paging.page * paging.size,
		status,
		sort,
		ascending,
		enabled: ready && statusReady,
	});
	const scatter = useListTraces({
		...filters,
		limit: SCATTER_LIMIT,
		offset: 0,
		status,
		sort: TraceSummarySort.START_TIME,
		enabled: ready && statusReady,
	});
	const countOf = (countStatus: TraceStatus) => ({
		...filters,
		limit: COUNT_LIMIT,
		offset: 0,
		status: countStatus,
		sort: TraceSummarySort.START_TIME,
		enabled: ready && !(statusReady && status === countStatus),
	});
	const allCount = useListTraces(countOf(TraceStatus.UNSPECIFIED));
	const errorCount = useListTraces(countOf(TraceStatus.ERROR));
	const openId = params.get("trace") ?? "";
	const detail = useGetTrace({
		workspaceId,
		environmentId,
		resourceIds: scopedResourceIds ?? [],
		traceId: openId,
		enabled: openId !== "" && TRACE_ID_PATTERN.test(openId) && scopeReady,
	});

	const traces = ready && statusReady ? (browse.data?.summaries ?? []) : [];
	const total = ready && statusReady ? Number(browse.data?.totalCount ?? 0n) : 0;
	const points = (ready && statusReady ? (scatter.data?.summaries ?? []) : []).map((t) => ({
		id: t.traceId,
		t: timestampMs(t.startTime),
		nanos: t.durationNanos,
		error: t.status === TraceStatus.ERROR,
	}));
	const countLabel = (countStatus: TraceStatus, data: { totalCount: bigint } | undefined) => {
		const counted = statusReady && status === countStatus ? browse.data : data;
		return ready && counted !== undefined ? formatCount(Number(counted.totalCount)) : undefined;
	};
	const errorsOnly = tokens.some((t) => sameToken(t, ERROR_TOKEN));
	const go = (page: number) => () => {
		setPaging({ page, size: paging.size }, DEFAULT_PAGE_SIZE);
	};
	const pages = Math.max(1, Math.ceil(total / paging.size));
	const loading = environmentLoading || browse.isLoading || ((resourceFilter || !scopeReady) && resourcesLoading);
	const detailSpans = uniqueSpans(detail.data?.spans ?? []);
	const openTrace = summarizeTraces(detailSpans)[0];
	const query = queryStr(tokens, text);
	const open = (id: string) => {
		const next = new URLSearchParams(params);
		next.set("trace", id);
		next.delete("span");
		setParams(next);
	};
	const close = () => {
		const next = new URLSearchParams(params);
		next.delete("trace");
		next.delete("span");
		setParams(next);
	};
	const crossesDay = new Date(bounds.from).toDateString() !== new Date(bounds.to).toDateString();
	const valueCounts = (key: FieldKey): [string, number][] => {
		if (key === "resource") return resources.map((r) => [r.name, 0]);
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
				[">1s", 0],
			];
		return [];
	};

	const zoom = (z: ScatterZoom) => {
		setZoomPrev(zoomPrev ?? { range, durations: tokens.filter((t) => t.key === "duration") });
		const rest = tokens.filter((t) => t.key !== "duration");
		const next = writeQuery(
			writeWindow(params, { kind: "absolute", from: z.from, to: z.to }),
			[...rest, durationToken(">=", z.minMs), durationToken("<=", z.maxMs)],
			text,
		);
		next.delete("page");
		setParams(next);
	};
	const resetZoom = () => {
		if (zoomPrev === null) return;
		const rest = tokens.filter((t) => t.key !== "duration");
		const next = writeQuery(writeWindow(params, zoomPrev.range), [...rest, ...zoomPrev.durations], text);
		next.delete("page");
		setZoomPrev(null);
		setParams(next);
	};
	const sortBy = (column: TraceSummarySort) => {
		const next = new URLSearchParams(params);
		const flip = column === sort && !ascending;
		if (column === TraceSummarySort.DURATION) next.set("sort", "duration");
		else next.delete("sort");
		if (flip) next.set("dir", "asc");
		else next.delete("dir");
		next.delete("page");
		setParams(next);
	};
	const sortDir = (column: TraceSummarySort) => (sort !== column ? undefined : ascending ? "ascending" : "descending");
	const widths = readWidths(params.get("widths"));

	if (openId !== "" && active !== undefined && params.get("env") === null) {
		const pinned = new URLSearchParams(params);
		pinned.set("env", active.name);
		return <Navigate to={{ search: `?${pinned.toString()}` }} replace />;
	}

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
						setParam("page", null, true);
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
	if (!loading && browse.error === null && total === 0 && query === "") {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ObsHero
					view="traces"
					title="Send your first trace"
					body="Loco already collects traces. Add an OpenTelemetry SDK to your app and your traces will show up here."
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
			{!validLookup && (
				<p role="alert" className="m-0 text-[12.5px] text-bad-fg">
					A trace ID is 32 lowercase hexadecimal characters.
				</p>
			)}
			{duration === null && (
				<p role="alert" className="m-0 text-[12.5px] text-bad-fg">
					A duration filter compares in milliseconds, like @duration:&gt;500, or with a unit, like @duration:&lt;2s.
				</p>
			)}
			<Section
				title="Duration"
				actions={
					zoomPrev !== null && (
						<Button variant="ghost" size="sm" onClick={resetZoom}>
							<ZoomOutIcon />
							Reset zoom
						</Button>
					)
				}
			>
				<div className="px-5 pt-3.5 pb-2.5">
					<TraceScatter points={points} from={bounds.from} to={bounds.to} selectedId={openId} onZoom={zoom} />
				</div>
			</Section>
			<section aria-label="Trace list" className="flex min-w-0 flex-col rounded-card bg-panel">
				<div className="flex flex-wrap items-center gap-2 px-5 pt-3 pb-1.5">
					<ToggleGroup
						variant="segmented"
						aria-label="Show"
						value={[errorsOnly ? "errors" : "all"]}
						onValueChange={(v: string[]) => {
							const rest = tokens.filter((t) => t.key !== "status");
							setQuery(v[0] === "errors" ? [...rest, ERROR_TOKEN] : rest, text);
						}}
					>
						<ToggleGroupItem value="all" count={countLabel(TraceStatus.UNSPECIFIED, allCount.data)}>
							All
						</ToggleGroupItem>
						<ToggleGroupItem value="errors" count={countLabel(TraceStatus.ERROR, errorCount.data)}>
							Errors
						</ToggleGroupItem>
					</ToggleGroup>
				</div>
				<div className="overflow-x-auto px-5 [scrollbar-width:thin]">
					<TraceList
						traces={traces}
						loading={loading}
						openId={openId}
						crossesDay={crossesDay}
						resourceName={(t) => traceServiceLabel(t, resourceById.get(t.resourceId)?.name)}
						onOpen={open}
						widths={widths}
						onWidths={(next) => {
							setParam("widths", writeWidths(next), true);
						}}
						sortDir={sortDir}
						onSort={sortBy}
					/>
					{!loading && browse.error !== null && (
						<div role="alert" className="flex flex-wrap items-center justify-between gap-3 px-3 py-6">
							<span className="text-bad-fg">{getErrorMessage(browse.error, "Failed to load traces")}</span>
							<Button
								variant="outline"
								onClick={() => {
									void browse.refetch();
								}}
							>
								Retry
							</Button>
						</div>
					)}
					{!loading && browse.error === null && traces.length === 0 && (
						<NoMatches title={filterReady ? "No requests match." : "The filtered resources aren't in this workspace"} />
					)}
				</div>
				<Pager
					label={traces.length > 0 ? pageRangeLabel(paging.page, paging.size, total) : "0"}
					total={total}
					pageSizes={PAGE_SIZES}
					pageSize={paging.size}
					onPageSize={(size) => {
						setPaging({ page: 0, size }, DEFAULT_PAGE_SIZE);
					}}
					onFirst={!loading && paging.page > 0 ? go(0) : null}
					onPrev={!loading && paging.page > 0 ? go(paging.page - 1) : null}
					onNext={!loading && paging.page < pages - 1 ? go(paging.page + 1) : null}
					onLast={!loading && paging.page < pages - 1 ? go(pages - 1) : null}
				/>
			</section>
			{openId !== "" && (
				<TraceDrawer
					traceId={openId}
					trace={openTrace}
					loading={detail.isLoading}
					error={detail.error}
					onRetry={() => {
						void detail.refetch();
					}}
					onClose={close}
				/>
			)}
		</>
	);
}

function TraceList({
	traces,
	loading,
	openId,
	crossesDay,
	resourceName,
	onOpen,
	widths,
	onWidths,
	sortDir,
	onSort,
}: {
	traces: TraceSummary[];
	loading: boolean;
	openId: string;
	crossesDay: boolean;
	resourceName: (t: TraceSummary) => string;
	onOpen: (id: string) => void;
	widths: ColumnWidths;
	onWidths: (next: ColumnWidths) => void;
	sortDir: (column: TraceSummarySort) => "ascending" | "descending" | undefined;
	onSort: (column: TraceSummarySort) => void;
}) {
	const { live, handle } = useResizableColumns(widths, onWidths, traceColumnWidth);
	const grid = [...TRACE_COLUMNS.map((c) => `${String(traceColumnWidth(c.key, live))}px`), "minmax(0,1fr)"].join(" ");
	return (
		<div className="w-max min-w-full">
			<div className={LIST_HEAD_CLASS} style={{ gridTemplateColumns: grid }}>
				{TRACE_COLUMNS.map((c) => (
					<span
						key={c.key}
						title={c.title}
						className={cn("relative flex min-w-0 items-center px-3", c.right === true && "justify-end")}
					>
						{c.sort === undefined ? (
							<span className="truncate">{c.label}</span>
						) : (
							<button
								type="button"
								onClick={() => {
									if (c.sort !== undefined) onSort(c.sort);
								}}
								className="truncate rounded-sm hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
							>
								{c.label}
								{sortDir(c.sort) !== undefined && (
									<>
										<span aria-hidden>{sortDir(c.sort) === "ascending" ? " ↑" : " ↓"}</span>
										<span className="sr-only">, sorted {sortDir(c.sort)}</span>
									</>
								)}
							</button>
						)}
						{handle(c.key, c.label)}
					</span>
				))}
				<span />
			</div>
			<div className="flex flex-col gap-0.5 pt-1">
				{loading &&
					Array.from({ length: SKELETON_ROWS }, (_, i) => (
						<div key={String(i)} className="flex h-[34px] items-center px-3">
							<Skeleton className="h-3 w-full" />
						</div>
					))}
				{!loading &&
					traces.map((t, i) => {
						const failed = t.status === TraceStatus.ERROR;
						const startMs = timestampMs(t.startTime);
						const name = t.rootSpanName || "Unnamed span";
						return (
							<button
								key={t.traceId}
								type="button"
								aria-label={`Open trace ${t.rootSpanName || t.traceId}`}
								aria-pressed={t.traceId === openId}
								onClick={() => {
									onOpen(t.traceId);
								}}
								className={listRowClass(i, t.traceId === openId)}
								style={{ gridTemplateColumns: grid }}
							>
								<span className="truncate px-3 font-mono whitespace-nowrap text-fg3">
									{crossesDay ? fmtTs(startMs) : formatClock(startMs)}
								</span>
								<span className="truncate px-3 text-[12.5px]">{resourceName(t)}</span>
								<span className="truncate px-3 font-mono text-foreground">{name}</span>
								<span className={cn("truncate px-3 font-mono", failed ? "text-bad-fg" : "text-fg3")}>
									{failed ? "error" : "ok"}
								</span>
								<span className={cn("truncate px-3 text-right font-mono", failed ? "text-bad-fg" : "text-foreground")}>
									{durationLabel(t.durationNanos)}
								</span>
								<span className="truncate px-3 text-right font-mono text-fg3">{String(t.spanCount)}</span>
								<span />
							</button>
						);
					})}
			</div>
		</div>
	);
}
