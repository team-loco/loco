import { useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { ArrowLeftIcon, ArrowRightIcon, CheckIcon, CopyIcon, RefreshCwIcon, SearchIcon, WaypointsIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Pager } from "@/components/design/Pager";
import { Input } from "@/components/design/Input";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useQueryTraces } from "@/hooks/useQueryTraces";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage } from "@/lib/error-handler";
import { timeRangeMs, type TimeRange } from "@/lib/obs";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";

import { QUERY_MAX_RANGE, RANGES, useObs } from "./context";
import { ResourceMenu, TimeRangeMenu } from "./Toolbar";
import { TRACE_ID_PATTERN, durationLabel, summarizeTraces, uniqueSpans, type TraceSummary } from "./traces";
import { TraceWaterfall } from "./TraceWaterfall";

const ROWS_PER_PAGE = 25;

export function TracesView() {
	const { workspaceId } = useObs();
	const [initialNow] = useState(Date.now);
	const [params] = useSearchParams();
	const requested = params.get("range");
	const range = RANGES.find((option) => option.key === requested && option.key !== "7d")?.key ?? "1h";
	const { active, isLoading } = useEnvironments();
	const end = Number(params.get("end"));
	const start = Number(params.get("start"));
	const validBounds = params.get("range") === range && Number.isSafeInteger(start) && Number.isSafeInteger(end) && start > 0 && end >= start && end - start <= timeRangeMs(QUERY_MAX_RANGE);
	if (active === undefined) return <div role="status" className="py-8 text-fg3">{isLoading ? "Loading environments…" : "No environments available"}</div>;
	if (!validBounds || params.get("env") !== active.id) {
		const now = initialNow;
		const canonical = new URLSearchParams(params);
		canonical.set("range", range);
		if (!validBounds) { canonical.set("start", String(now - timeRangeMs(range))); canonical.set("end", String(now)); }
		canonical.set("env", active.id);
		return <Navigate to={{ search: `?${canonical.toString()}` }} replace />;
	}
	return <TraceSession
		key={`${workspaceId}:${active.id}:${range}`}
		environmentId={active.id}
		environmentLoading={isLoading}
		range={range} />;
}

function TraceSession({ environmentId, environmentLoading, range }: { environmentId: string; environmentLoading: boolean; range: TimeRange; }) {
	const { workspaceId, resources, resourcesLoading } = useObs();
	const [params, setParams] = useSearchParams();
	const traceId = params.get("trace") ?? "";
	const selected = params.has("r") ? (params.get("r") ?? "").split(",").filter(Boolean) : null;
	const setSelected = (names: string[] | null) => { const next = new URLSearchParams(params); if (names === null) next.delete("r"); else next.set("r", names.join(",")); setParams(next); setPage(0); };
	const [input, setInput] = useState(traceId);
	const [invalid, setInvalid] = useState(false);
	const [previousTrace, setPreviousTrace] = useState(traceId);
	if (previousTrace !== traceId) { setPreviousTrace(traceId); setInput(traceId); setInvalid(false); }
	const [page, setPage] = useState(0);
	const bounds = { start: Number(params.get("start")), end: Number(params.get("end")) };
	const resourceIds = selected === null ? [] : resources.filter((resource) => selected.includes(resource.name)).map((resource) => resource.id);
	const filterReady = selected === null || (!resourcesLoading && selected.length > 0 && resourceIds.length === selected.length);
	const validTrace = traceId === "" || TRACE_ID_PATTERN.test(traceId);
	const browse = useQueryTraces({ workspaceId, environmentId, resourceIds, traceId: "", ...bounds, enabled: traceId === "" && filterReady });
	const detail = useQueryTraces({ workspaceId, environmentId, resourceIds: [], traceId, ...bounds, enabled: traceId !== "" && validTrace });
	const query = traceId === "" ? browse : detail;
	const spans = uniqueSpans(query.data?.pages.flatMap((result) => result.spans) ?? []);
	const traces = summarizeTraces(spans);
	const trace = traces[0];
	const [copied, copy] = useCopy();
	const loading = environmentLoading || query.isLoading || (traceId === "" && selected !== null && resourcesLoading);
	const incomplete = query.hasNextPage;
	const pageCount = Math.max(1, Math.ceil(traces.length / ROWS_PER_PAGE));
	const currentPage = Math.min(page, pageCount - 1);
	const update = (id: string) => {
		const next = new URLSearchParams(params);
		next.set("range", range);
		next.set("start", String(bounds.start));
		next.set("end", String(bounds.end));
		next.delete("span");
		if (id === "") next.delete("trace"); else next.set("trace", id);
		setParams(next);
		setInput(id);
		setInvalid(false);
	};
	return <>
		<form
			noValidate
			className="flex flex-wrap items-start gap-2"
			onSubmit={(event) => {
				event.preventDefault();
				const id = input.trim().toLowerCase();
				if (id !== "" && !TRACE_ID_PATTERN.test(id)) { setInvalid(true); return; }
				update(id);
			}}>
			<div className="min-w-[220px] flex-1">
				<div className="relative flex items-center">
					<SearchIcon aria-hidden className="absolute left-3 size-3.5 text-fg3" />
					<Input
						aria-label="Trace ID"
						aria-invalid={invalid || !validTrace}
						aria-describedby={invalid || !validTrace ? "trace-id-error" : undefined}
						value={input}
						placeholder="Find a trace by its ID"
						className="h-[38px] rounded-lg pr-9 pl-9 font-mono text-sm"
						onChange={(event) => { setInput(event.target.value); setInvalid(false); }} />
					{input !== "" && <Button
						type="button"
						variant="ghost"
						size="icon-xs"
						className="absolute right-2"
						aria-label="Clear trace search"
						onClick={() => { update(""); document.querySelector<HTMLInputElement>('input[aria-label="Trace ID"]')?.focus(); }}>
						<XIcon />
					</Button>}
				</div>
				{(invalid || !validTrace) && <p id="trace-id-error" className="mt-1 text-sm text-bad-fg">Enter a 32-character hexadecimal trace ID.</p>}
			</div>
			<Button type="submit" variant="outline" className="h-[38px] rounded-lg">Search</Button>
			<TimeRangeMenu
				maxRange={QUERY_MAX_RANGE}
				value={range}
				onChange={(value) => {
					const now = Date.now(); const next = new URLSearchParams(params); next.set("range", value); next.set("start", String(now - timeRangeMs(value))); next.set("end", String(now)); setParams(next);
				}} />
			<Button
				type="button"
				variant="outline"
				className="h-[38px] rounded-lg"
				disabled={query.isFetching}
				onClick={() => {
					const now = Date.now(); const next = new URLSearchParams(params); next.set("start", String(now - timeRangeMs(range))); next.set("end", String(now)); setParams(next); setPage(0);
				}}>
				<RefreshCwIcon className={cn(query.isFetching && "animate-spin motion-reduce:animate-none")} />Refresh
			</Button>
		</form>
		{traceId !== "" ? <>
			<div className="flex flex-wrap items-center gap-3">
				<Button variant="ghost" className="-ml-2" onClick={() => { update(""); }}>
					<ArrowLeftIcon />All traces</Button>
				<span className="text-sm text-fg3">All resources in this workspace and environment</span>
			</div>
			<section className="min-w-0 rounded-lg border border-line bg-background">
				<div className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-4">
					<div className="min-w-0">
						<div className="mb-1.5 flex items-center gap-2 text-sm text-fg3">
							<WaypointsIcon className="size-3.5" />TRACE</div>
						<h2 className="text-lg font-semibold break-words">{trace?.root.name ?? "Trace details"}</h2>
						<div className="mt-1 flex items-center gap-2">
							<code className="min-w-0 text-sm break-all text-fg3">{traceId}</code>
							<Button variant="ghost" size="icon-xs" aria-label="Copy trace ID" onClick={() => { copy("trace", traceId); }}>{copied === "trace" ? <CheckIcon /> : <CopyIcon />}
							</Button>
						</div>
					</div>
					{trace !== undefined && <dl className="flex flex-wrap gap-x-6 gap-y-2">
						<Summary label="Observed duration" value={durationLabel(trace.duration)} />
						<Summary label="Loaded spans" value={String(spans.length)} />
						<Summary label="Services" value={String(trace.services)} />
						<Summary label="Error spans" value={String(trace.errors)} bad={trace.errors > 0} />
					</dl>}
				</div>
				<QueryState loading={loading} error={query.error} empty={spans.length === 0} onRetry={() => { void query.refetch(); }} />
				{trace !== undefined && <TraceWaterfall key={traceId} trace={trace} incomplete={incomplete} />}
				<PageStatus
					count={spans.length}
					incomplete={incomplete}
					fetching={query.isFetchingNextPage}
					onMore={() => { void query.fetchNextPage(); }}
					detail />
			</section>
		</> : <>
			<div className="flex flex-wrap items-center justify-between gap-2">
				<ResourceMenu value={selected} onChange={setSelected} />
				<span className="text-sm text-fg3">{formatClock(bounds.start)}–{formatClock(bounds.end)} · Oldest spans first</span>
			</div>
			<section className="min-w-0 overflow-hidden rounded-lg border border-line bg-background">
				<div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-3">
					<h2 className="font-semibold">{traces.length} traces <span className="font-normal text-fg3">in {spans.length} loaded spans</span>
					</h2>
					<span className="text-sm text-fg3">{incomplete ? "Partial results" : "Within selected time range"}</span>
				</div>
				{!filterReady && !resourcesLoading && <div role="status" className="px-4 py-6 text-fg3">The selected resources are unavailable. <Button variant="outline" size="sm" onClick={() => { setSelected(null); }}>Show all resources</Button>
				</div>}
				<QueryState loading={loading} error={query.error} empty={spans.length === 0 && filterReady} onRetry={() => { void query.refetch(); }} />
				{spans.length > 0 && <div className="divide-y divide-line sm:hidden">{traces.slice(currentPage * ROWS_PER_PAGE, (currentPage + 1) * ROWS_PER_PAGE).map((item) => <button key={item.id} type="button" onClick={() => { update(item.id); }} className="block w-full px-4 py-3 text-left hover:bg-bg2 focus-visible:outline-2 focus-visible:outline-offset-[-2px]">
					<span className="block truncate font-medium">{item.root.name}</span>
					<span className="mt-1 block truncate text-sm text-fg3">{item.root.serviceName} · <span className="font-mono">{item.id.slice(0, 12)}</span></span>
					<span className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-fg3"><span className="font-mono text-foreground">{durationLabel(item.duration)}</span><span>{item.spans.length} loaded spans</span>{item.errors > 0 && <span className="text-bad-fg">{item.errors} error {item.errors === 1 ? "span" : "spans"}</span>}</span>
				</button>)}</div>}
				{spans.length > 0 && <div className="hidden overflow-x-auto sm:block">
					<table className="w-full min-w-[650px] text-left">
						<thead className="bg-bg2 text-sm font-normal text-fg3">
							<tr>
								<th className="px-4 py-2 font-normal">Operation / trace</th>
								<th className="px-3 py-2 font-normal">Start</th>
								<th className="px-3 py-2 font-normal">Observed duration</th>
								<th className="px-3 py-2 font-normal">Loaded spans</th>
								<th className="px-3 py-2 font-normal">Status</th>
								<th>
									<span className="sr-only">Open trace</span>
								</th>
							</tr>
						</thead>
						<tbody>{traces.slice(currentPage * ROWS_PER_PAGE, (currentPage + 1) * ROWS_PER_PAGE).map((item) => <TraceListRow key={item.id} trace={item} onOpen={() => { update(item.id); }} />)}
						</tbody>
					</table>
				</div>}
				{traces.length > ROWS_PER_PAGE && <Pager
					className="border-t border-line"
					label={`Page ${String(currentPage + 1)} of ${String(pageCount)} loaded results`}
					onPrev={currentPage === 0 ? null : () => { setPage(currentPage - 1); }}
					onNext={currentPage === pageCount - 1 ? null : () => { setPage(currentPage + 1); }} />}
				<PageStatus
					count={spans.length}
					incomplete={incomplete}
					fetching={query.isFetchingNextPage}
					onMore={() => { void query.fetchNextPage(); }} />
			</section>
		</>}
	</>;
}

function Summary({ label, value, bad = false }: { label: string; value: string; bad?: boolean; }) {
	return <div>
		<dt className="text-sm text-fg3">{label}</dt>
		<dd className={cn("mt-1 font-mono text-lg tabular-nums", bad && "text-bad-fg")}>{value}</dd>
	</div>;
}

function TraceListRow({ trace, onOpen }: { trace: TraceSummary; onOpen: () => void; }) {
	return <tr className="group border-t border-line hover:bg-bg2">
		<td className="max-w-[360px] px-4 py-3">
			<button
				type="button"
				onClick={onOpen}
				className="block max-w-full text-left hover:underline focus-visible:outline-2 focus-visible:outline-offset-4">
				<span className="block truncate font-medium">{trace.root.name}</span>
				<span className="mt-1 block truncate text-sm text-fg3">{trace.root.serviceName} <span className="px-1 text-fg4">/</span> <span className="font-mono">{trace.id.slice(0, 16)}</span>
				</span>
			</button>
		</td>
		<td className="px-3 py-3 font-mono text-sm text-fg3">{formatClock(Number(trace.start / 1_000_000n), true)}</td>
		<td className="px-3 py-3 font-mono tabular-nums">{durationLabel(trace.duration)}</td>
		<td className="px-3 py-3 text-sm text-fg3">{trace.spans.length} · {trace.services} {trace.services === 1 ? "service" : "services"}</td>
		<td className="px-3 py-3 text-sm">{trace.errors > 0 ? <span className="text-bad-fg">{trace.errors} error {trace.errors === 1 ? "span" : "spans"}</span> : <span className="text-fg3">No errors observed</span>}
		</td>
		<td className="pr-3">
			<Button variant="ghost" size="icon-xs" aria-label={`Open trace ${trace.id}`} onClick={onOpen}>
				<ArrowRightIcon className="text-fg3" />
			</Button>
		</td>
	</tr>;
}

function QueryState({ loading, error, empty, onRetry }: { loading: boolean; error: Error | null; empty: boolean; onRetry: () => void; }) {
	if (loading) return <div role="status" className="flex min-h-[200px] items-center justify-center gap-2 text-fg3">
		<RefreshCwIcon className="size-4 animate-spin motion-reduce:animate-none" />Loading spans…
	</div>;
	if (error !== null) return <div role="alert" className="flex flex-wrap items-center justify-between gap-3 px-4 py-6">
		<span className="text-bad-fg">{getErrorMessage(error, "Failed to load traces")}</span>
		<Button variant="outline" onClick={onRetry}>Retry</Button>
	</div>;
	if (empty) return <EmptyState icon={<WaypointsIcon />} title="No traces in this time range">Choose a wider time range or send traces from your application through OTLP.
	</EmptyState>;
	return null;
}

function PageStatus({ count, incomplete, fetching, onMore, detail = false }: { count: number; incomplete: boolean; fetching: boolean; onMore: () => void; detail?: boolean; }) {
	if (count === 0) return null;
	return <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line bg-bg2 px-4 py-3 text-sm text-fg3">
		<span>{incomplete ? "More spans are available. Counts and durations reflect loaded spans." : detail ? "All available spans in this time range are loaded. Sampling or retention may leave gaps." : "All spans in this time range are loaded."}
		</span>{incomplete && <Button variant="outline" size="sm" disabled={fetching} onClick={onMore}>{fetching ? "Loading…" : "Load more spans"}</Button>}
	</div>;
}
