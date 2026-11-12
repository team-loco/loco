import { TraceStatus, TraceSummarySort } from "@gen/loco/observability/v1/observability_pb";
import { useState } from "react";
import { RefreshCwIcon } from "lucide-react";
import { Navigate } from "react-router";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useGetTrace } from "@/hooks/useGetTrace";
import { useListTraces } from "@/hooks/useListTraces";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { resolveWindow } from "@/lib/obs-time";
import { pageRangeLabel } from "@/lib/paging";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";

import { filteredResources, useObs } from "./context";
import { fmtTs } from "./format";
import { NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import { liveTokens, passes, queryStr, sameToken, type FieldKey } from "./query";
import { QueryBar } from "./QueryBar";
import { PAGE_SIZES, readPaging } from "./shared";
import { TraceDrawer } from "./TraceDrawer";
import { traceServiceLabel } from "./trace-service";
import { ViewHeader } from "./ViewHeader";
import { TRACE_ID_PATTERN, durationLabel, summarizeTraces, uniqueSpans } from "./traces";

const DEFAULT_PAGE_SIZE = 25;
const PERCENT = 100n;
const ERROR_TOKEN = { neg: false, key: "status" as const, value: "error" };

export function TracesView() {
	const { range } = useObs();
	return <TraceSession key={JSON.stringify(range)} />;
}

function TraceSession() {
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
	const sort = params.get("sort") === "duration" ? TraceSummarySort.DURATION : TraceSummarySort.START_TIME;
	const browse = useListTraces({
		workspaceId,
		environmentId,
		resourceIds: resourceFilter ? visible.map((r) => r.id) : (scopedResourceIds ?? []),
		traceId: validLookup ? lookup : "",
		start: bounds.from,
		end: bounds.to,
		limit: paging.size,
		offset: paging.page * paging.size,
		rootNameTerms: text.split(" ").filter(Boolean),
		status:
			statuses.length === 1 ? (statuses[0] === "error" ? TraceStatus.ERROR : TraceStatus.OK) : TraceStatus.UNSPECIFIED,
		sort,
		enabled: filterReady && scopeReady && validLookup && statusReady,
	});
	const openId = params.get("trace") ?? "";
	const detail = useGetTrace({
		workspaceId,
		environmentId,
		resourceIds: scopedResourceIds ?? [],
		traceId: openId,
		enabled: openId !== "" && TRACE_ID_PATTERN.test(openId) && scopeReady,
	});

	const traces = filterReady && scopeReady && validLookup && statusReady ? (browse.data?.summaries ?? []) : [];
	const total = filterReady && scopeReady && validLookup && statusReady ? Number(browse.data?.totalCount ?? 0n) : 0;
	const errorsOnly = tokens.some((t) => sameToken(t, ERROR_TOKEN));
	const go = (page: number) => () => {
		setPaging({ page, size: paging.size }, DEFAULT_PAGE_SIZE);
	};
	const pages = Math.max(1, Math.ceil(total / paging.size));
	const maxDuration = traces.reduce((max, t) => (t.durationNanos > max ? t.durationNanos : max), 1n);
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
		return [];
	};

	if (openId !== "" && active !== undefined && params.get("env") === null) {
		const pinned = new URLSearchParams(params);
		pinned.set("env", active.name);
		return <Navigate to={{ search: `?${pinned.toString()}` }} replace />;
	}

	const header = (
		<ViewHeader
			count={loading || browse.error !== null ? undefined : formatCount(total)}
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
					title="No traces in this range"
					body="Every replica is already pointed at a trace collector. Add an OpenTelemetry SDK to your app and its traces show up here."
					action={<WidenRangeButton />}
				/>
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
			<Section
				title={
					<>
						Duration
						<SoonTag />
					</>
				}
				card={false}
			/>
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
						<ToggleGroupItem value="all">All</ToggleGroupItem>
						<ToggleGroupItem value="errors">Errors</ToggleGroupItem>
					</ToggleGroup>
				</div>
				<div className="overflow-x-auto px-5 [scrollbar-width:thin]">
					<table className="w-full min-w-[720px] border-collapse text-[12.5px]">
						<thead>
							<tr className="h-[38px] border-b border-line2 text-left text-[12px] text-fg3">
								<th
									className="px-3 font-normal"
									aria-sort={sort === TraceSummarySort.START_TIME ? "descending" : "none"}
								>
									<button
										type="button"
										onClick={() => {
											const next = new URLSearchParams(params);
											next.delete("sort");
											next.delete("page");
											setParams(next);
										}}
									>
										When
									</button>
								</th>
								<th className="px-3 font-normal">Resource</th>
								<th className="px-3 font-normal">Root span</th>
								<th className="px-3 font-normal">Status</th>
								<th
									className="px-3 text-right font-normal"
									aria-sort={sort === TraceSummarySort.DURATION ? "descending" : "none"}
									title="From the first span's start to the last span's end"
								>
									<button
										type="button"
										onClick={() => {
											const next = new URLSearchParams(params);
											next.set("sort", "duration");
											next.delete("page");
											setParams(next);
										}}
									>
										Observed duration
									</button>
								</th>
								<th className="px-3 text-right font-normal">Spans</th>
							</tr>
						</thead>
						<tbody>
							{loading &&
								Array.from({ length: 8 }, (_, i) => (
									<tr key={String(i)} className="h-[38px]">
										<td colSpan={6} className="px-3">
											<Skeleton className="h-3 w-full" />
										</td>
									</tr>
								))}
							{!loading &&
								traces.map((t) => {
									const failed = t.status === TraceStatus.ERROR;
									const startMs = Number(t.startTime?.seconds ?? 0n) * 1000 + (t.startTime?.nanos ?? 0) / 1_000_000;
									const on = t.traceId === openId;
									return (
										<tr
											key={t.traceId}
											className={cn(
												"h-[38px] border-b border-line last:border-b-0 hover:bg-panel2",
												on && "bg-background",
											)}
										>
											<td className="px-3 font-mono whitespace-nowrap text-fg3">
												{crossesDay ? fmtTs(startMs) : formatClock(startMs)}
											</td>
											<td className="px-3">{traceServiceLabel(t, resourceById.get(t.resourceId)?.name)}</td>
											<td className="max-w-[360px] px-3">
												<button
													type="button"
													aria-label={`Open trace ${t.rootSpanName || t.traceId}`}
													onClick={() => {
														open(t.traceId);
													}}
													className="block w-full truncate rounded-sm text-left font-mono text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
												>
													{t.rootSpanName || "Unnamed span"}
												</button>
											</td>
											<td className={cn("px-3 font-mono", failed ? "text-bad-fg" : "text-fg3")}>
												{failed ? "error" : "ok"}
											</td>
											<td className="px-3">
												<span className="flex items-center justify-end gap-2">
													<span className="h-1 w-20 overflow-hidden rounded-[2px] bg-line2 max-sm:hidden">
														<span
															className={cn("block h-full rounded-[2px]", failed ? "bg-err" : "bg-fg3")}
															style={{ width: `${String(Number((t.durationNanos * PERCENT) / maxDuration))}%` }}
														/>
													</span>
													<span className={cn("font-mono", failed ? "text-bad-fg" : "text-foreground")}>
														{durationLabel(t.durationNanos)}
													</span>
												</span>
											</td>
											<td className="relative px-3 text-right font-mono text-fg3">
												{String(t.spanCount)}
												{failed && <span className="sr-only">, with errors</span>}
											</td>
										</tr>
									);
								})}
						</tbody>
					</table>
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
						<NoMatches
							title={filterReady ? "No traces match these filters" : "The filtered resources aren't in this workspace"}
						/>
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
