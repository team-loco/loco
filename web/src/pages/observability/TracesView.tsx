import { useState } from "react";
import { RefreshCwIcon } from "lucide-react";
import { Navigate } from "react-router";

import { Button } from "@/components/design/Button";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useQueryTraces } from "@/hooks/useQueryTraces";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { resolveWindow } from "@/lib/obs-time";
import { pagedRows } from "@/lib/paging";
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
import { TRACE_ID_PATTERN, durationLabel, spanError, summarizeTraces, uniqueSpans, type TraceSummary } from "./traces";

const DEFAULT_PAGE_SIZE = 25;
const NANOS_PER_MS = 1_000_000n;
const PERCENT = 100n;
const ERROR_TOKEN = { neg: false, key: "status" as const, value: "error" };

function statusOf(trace: TraceSummary): string {
	return trace.errors > 0 ? "error" : "ok";
}

export function TracesView() {
	const { range } = useObs();
	return <TraceSession key={JSON.stringify(range)} />;
}

function TraceSession() {
	const {
		workspaceId,
		resources,
		resourcesLoading,
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
	const browse = useQueryTraces({
		workspaceId,
		environmentId,
		resourceIds: resourceFilter ? visible.map((r) => r.id) : [],
		traceId: validLookup ? lookup : "",
		start: bounds.from,
		end: bounds.to,
		enabled: filterReady && validLookup,
	});
	const openId = params.get("trace") ?? "";
	const detail = useQueryTraces({
		workspaceId,
		environmentId,
		resourceIds: [],
		traceId: openId,
		start: bounds.from,
		end: bounds.to,
		enabled: openId !== "" && TRACE_ID_PATTERN.test(openId),
	});

	const spans = uniqueSpans(browse.data?.pages.flatMap((r) => r.spans) ?? []);
	const words = text.toLowerCase().split(" ").filter(Boolean);
	const statusTokens = liveTokens("traces", tokens, "status");
	const named = summarizeTraces(spans).filter((t) =>
		words.every((w) => t.spans.some((s) => s.name.toLowerCase().includes(w))),
	);
	const traces = named
		.filter((t) => passes([statusOf(t)], statusTokens))
		.toSorted((a, b) => (a.start > b.start ? -1 : 1));
	const errorCount = named.filter((t) => t.errors > 0).length;
	const errorsOnly = tokens.some((t) => sameToken(t, ERROR_TOKEN));
	const paging = readPaging(params, DEFAULT_PAGE_SIZE);
	const paged = pagedRows(
		traces,
		paging,
		(next) => {
			setPaging(next, DEFAULT_PAGE_SIZE);
		},
		PAGE_SIZES,
		"0",
	);
	const maxDuration = traces.reduce((max, t) => (t.duration > max ? t.duration : max), 1n);
	const loading = environmentLoading || browse.isLoading || (resourceFilter && resourcesLoading);
	const detailSpans = uniqueSpans(detail.data?.pages.flatMap((r) => r.spans) ?? []);
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
				["error", errorCount],
				["ok", named.length - errorCount],
			];
		if (key === "trace") return traces.slice(0, paging.page + 1).map((t) => [t.id, t.spans.length]);
		return [];
	};

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
	if (!loading && browse.error === null && spans.length === 0 && query === "") {
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
			<section aria-labelledby="ts-duration" className="flex flex-col gap-2.5">
				<div className="flex flex-wrap items-baseline gap-3 pt-1.5">
					<h2 id="ts-duration" className="m-0 flex items-baseline gap-2 text-[18px] font-normal tracking-[-0.02em]">
						Duration
						<SoonTag />
					</h2>
					<span className="text-[12.5px] text-fg3">each trace as a dot by start time and duration</span>
				</div>
			</section>
			<section aria-label="Trace list" className="flex min-w-0 flex-col rounded-[10px] bg-panel">
				<div className="flex flex-wrap items-center gap-2 px-5 pt-3 pb-1.5">
					<span className="text-[13.5px]">
						{formatCount(traces.length)} {traces.length === 1 ? "trace" : "traces"}
					</span>
					<span className="text-[12.5px] text-fg3">
						observed in {formatCount(spans.length)} loaded {spans.length === 1 ? "span" : "spans"}
						{browse.hasNextPage ? " · partial" : ""}
					</span>
					<span className="flex-1" />
					<ToggleGroup
						variant="segmented"
						aria-label="Show"
						value={[errorsOnly ? "errors" : "all"]}
						onValueChange={(v: string[]) => {
							const rest = tokens.filter((t) => t.key !== "status");
							setQuery(v[0] === "errors" ? [...rest, ERROR_TOKEN] : rest, text);
						}}
					>
						<ToggleGroupItem value="all" count={formatCount(named.length)}>
							All
						</ToggleGroupItem>
						<ToggleGroupItem value="errors" count={formatCount(errorCount)}>
							Errors
						</ToggleGroupItem>
					</ToggleGroup>
				</div>
				<div className="overflow-x-auto px-5 [scrollbar-width:thin]">
					<table className="w-full min-w-[720px] border-collapse text-[12.5px]">
						<thead>
							<tr className="h-[38px] border-b border-line2 text-left text-[12px] text-fg3">
								<th className="px-3 font-normal">When</th>
								<th className="px-3 font-normal">Resource</th>
								<th className="px-3 font-normal">Root span</th>
								<th className="px-3 font-normal">Status</th>
								<th className="px-3 text-right font-normal" title="From the first span's start to the last span's end">
									Observed duration
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
								paged.rows.map((t) => {
									const failed = t.errors > 0;
									const startMs = Number(t.start / NANOS_PER_MS);
									const on = t.id === openId;
									return (
										<tr
											key={t.id}
											className={cn(
												"h-[38px] border-b border-line last:border-b-0 hover:bg-panel2",
												on && "bg-background",
											)}
										>
											<td className="px-3 font-mono whitespace-nowrap text-fg3">
												{crossesDay ? fmtTs(startMs) : formatClock(startMs)}
											</td>
											<td className="px-3">{traceServiceLabel(t.root, resourceById.get(t.root.resourceId)?.name)}</td>
											<td className="max-w-[360px] px-3">
												<button
													type="button"
													aria-label={`Open trace ${t.root.name || t.id}`}
													onClick={() => {
														open(t.id);
													}}
													className="block w-full truncate rounded-sm text-left font-mono text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
												>
													{t.root.name || "Unnamed span"}
												</button>
											</td>
											<td className={cn("px-3 font-mono", failed ? "text-bad-fg" : "text-fg3")}>
												{failed ? `error · ${String(t.errors)}` : "ok"}
											</td>
											<td className="px-3">
												<span className="flex items-center justify-end gap-2">
													<span className="h-1 w-20 overflow-hidden rounded-[2px] bg-line2 max-sm:hidden">
														<span
															className={cn("block h-full rounded-[2px]", failed ? "bg-err" : "bg-fg3")}
															style={{ width: `${String(Number((t.duration * PERCENT) / maxDuration))}%` }}
														/>
													</span>
													<span className={cn("font-mono", failed ? "text-bad-fg" : "text-foreground")}>
														{durationLabel(t.duration)}
													</span>
												</span>
											</td>
											<td className="relative px-3 text-right font-mono text-fg3">
												{t.spans.length}
												{t.spans.some(spanError) && <span className="sr-only">, with errors</span>}
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
				{browse.hasNextPage && (
					<div className="flex flex-wrap items-center justify-between gap-2 px-5 pt-2 text-[12px] text-fg3">
						<span>Partial results. Counts and durations reflect the loaded spans.</span>
						<Button
							variant="outline"
							size="sm"
							disabled={browse.isFetchingNextPage}
							onClick={() => {
								void browse.fetchNextPage();
							}}
						>
							{browse.isFetchingNextPage ? "Loading…" : "Load more spans"}
						</Button>
					</div>
				)}
				<Pager {...paged.pager} {...paged.ends} />
			</section>
			{openId !== "" && (
				<TraceDrawer
					traceId={openId}
					trace={openTrace}
					loading={detail.isLoading}
					error={detail.error}
					incomplete={detail.hasNextPage}
					loadingMore={detail.isFetchingNextPage}
					onLoadMore={() => {
						void detail.fetchNextPage();
					}}
					onRetry={() => {
						void detail.refetch();
					}}
					onClose={close}
				/>
			)}
		</>
	);
}
