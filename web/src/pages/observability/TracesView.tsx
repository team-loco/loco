import { useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import {
	ArrowLeftIcon,
	CheckIcon,
	CopyIcon,
	RefreshCwIcon,
	WaypointsIcon,
	XIcon,
} from "lucide-react";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Pager } from "@/components/design/Pager";
import { TraceSurface, TraceDot } from "@/components/design/Trace";
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
import {
	TRACE_ID_PATTERN,
	durationLabel,
	summarizeTraces,
	uniqueSpans,
} from "./traces";
import { traceServiceColor, traceServiceLabel } from "./trace-service";
import { TraceWaterfall } from "./TraceWaterfall";

const ROWS_PER_PAGE = 25;

export function TracesView() {
	const { workspaceId } = useObs();
	const [initialNow] = useState(Date.now);
	const [params] = useSearchParams();
	const requested = params.get("range");
	const range =
		RANGES.find((option) => option.key === requested && option.key !== "7d")
			?.key ?? "1h";
	const { active, isLoading } = useEnvironments();
	const end = Number(params.get("end"));
	const start = Number(params.get("start"));
	const validBounds =
		params.get("range") === range &&
		Number.isSafeInteger(start) &&
		Number.isSafeInteger(end) &&
		start > 0 &&
		end >= start &&
		end - start <= timeRangeMs(QUERY_MAX_RANGE);
	if (active === undefined)
		return (
			<div role="status" className="py-8 text-fg3">
				{isLoading ? "Loading environments…" : "No environments available"}
			</div>
		);
	if (!validBounds || params.get("env") !== active.id) {
		const now = initialNow;
		const canonical = new URLSearchParams(params);
		canonical.set("range", range);
		if (!validBounds) {
			canonical.set("start", String(now - timeRangeMs(range)));
			canonical.set("end", String(now));
		}
		canonical.set("env", active.id);
		return <Navigate to={{ search: `?${canonical.toString()}` }} replace />;
	}
	return (
		<TraceSession
			key={`${workspaceId}:${active.id}:${range}`}
			environmentId={active.id}
			environmentLoading={isLoading}
			range={range}
		/>
	);
}

function TraceSession({
	environmentId,
	environmentLoading,
	range,
}: {
	environmentId: string;
	environmentLoading: boolean;
	range: TimeRange;
}) {
	const { workspaceId, resources, resourcesLoading } = useObs();
	const [params, setParams] = useSearchParams();
	const traceId = params.get("trace") ?? "";
	const selected = params.has("r")
		? (params.get("r") ?? "").split(",").filter(Boolean)
		: null;
	const setSelected = (names: string[] | null) => {
		const next = new URLSearchParams(params);
		if (names === null) next.delete("r");
		else next.set("r", names.join(","));
		setParams(next);
		setPage(0);
	};
	const [input, setInput] = useState(traceId);
	const [invalid, setInvalid] = useState(false);
	const [previousTrace, setPreviousTrace] = useState(traceId);
	if (previousTrace !== traceId) {
		setPreviousTrace(traceId);
		setInput(traceId);
		setInvalid(false);
	}
	const [page, setPage] = useState(0);
	const bounds = {
		start: Number(params.get("start")),
		end: Number(params.get("end")),
	};
	const resourceIds =
		selected === null
			? []
			: resources
					.filter((resource) => selected.includes(resource.name))
					.map((resource) => resource.id);
	const filterReady =
		selected === null ||
		(!resourcesLoading &&
			selected.length > 0 &&
			resourceIds.length === selected.length);
	const validTrace = traceId === "" || TRACE_ID_PATTERN.test(traceId);
	const browse = useQueryTraces({
		workspaceId,
		environmentId,
		resourceIds,
		traceId: "",
		...bounds,
		enabled: traceId === "" && filterReady,
	});
	const detail = useQueryTraces({
		workspaceId,
		environmentId,
		resourceIds: [],
		traceId,
		...bounds,
		enabled: traceId !== "" && validTrace,
	});
	const query = traceId === "" ? browse : detail;
	const spans = uniqueSpans(
		query.data?.pages.flatMap((result) => result.spans) ?? [],
	);
	const traces = summarizeTraces(spans);
	const trace = traces[0];
	const [copied, copy] = useCopy();
	const loading =
		environmentLoading ||
		query.isLoading ||
		(traceId === "" && selected !== null && resourcesLoading);
	const incomplete = query.hasNextPage;
	const pageCount = Math.max(1, Math.ceil(traces.length / ROWS_PER_PAGE));
	const currentPage = Math.min(page, pageCount - 1);
	const update = (id: string) => {
		const next = new URLSearchParams(params);
		next.set("range", range);
		next.set("start", String(bounds.start));
		next.set("end", String(bounds.end));
		next.delete("span");
		if (id === "") next.delete("trace");
		else next.set("trace", id);
		setParams(next);
		setInput(id);
		setInvalid(false);
	};
	const rootLabel =
		trace === undefined
			? ""
			: traceServiceLabel(
					trace.root,
					resources.find((resource) => resource.id === trace.root.resourceId)
						?.name,
				);
	const maxDuration = traces.reduce(
		(max, item) => (item.duration > max ? item.duration : max),
		1n,
	);
	const controls = (
		<>
			<TimeRangeMenu
				compact
				maxRange={QUERY_MAX_RANGE}
				value={range}
				onChange={(value) => {
					const now = Date.now();
					const next = new URLSearchParams(params);
					next.set("range", value);
					next.set("start", String(now - timeRangeMs(value)));
					next.set("end", String(now));
					setParams(next);
				}}
			/>
			<Button
				type="button"
				variant="outline"
				size="icon-xs"
				aria-label="Refresh"
				disabled={query.isFetching}
				onClick={() => {
					const now = Date.now();
					const next = new URLSearchParams(params);
					next.set("start", String(now - timeRangeMs(range)));
					next.set("end", String(now));
					setParams(next);
					setPage(0);
				}}
			>
				<RefreshCwIcon
					className={cn(
						query.isFetching && "animate-spin motion-reduce:animate-none",
					)}
				/>
			</Button>
		</>
	);
	return (
		<div className="flex min-w-0 flex-col gap-2">
			{traceId !== "" ? (
				<>
					<div className="flex flex-wrap items-center justify-between gap-2">
						<div className="flex min-w-0 items-center gap-2">
							<Button
								variant="ghost"
								size="xs"
								className="-ml-2"
								onClick={() => {
									update("");
								}}
							>
								<ArrowLeftIcon />
								All traces
							</Button>
							<span className="text-[11px] text-fg3 max-sm:hidden">
								Workspace / environment · All resources
							</span>
						</div>
						<div className="flex items-center gap-1.5">{controls}</div>
					</div>
					<div className="flex min-w-0 items-start justify-between gap-3 pb-1">
						<div className="min-w-0">
							<h1 className="flex min-w-0 items-center gap-2 text-sm font-semibold">
								<TraceDot
									color={traceServiceColor(trace?.root.serviceName ?? "")}
								/>
								<span
									className="max-w-48 truncate text-fg3"
									title={trace?.root.serviceName}
								>
									{rootLabel}
								</span>
								<span className="truncate" title={trace?.root.name}>
									{trace?.root.name ?? "Trace details"}
								</span>
							</h1>
							<div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-fg3">
								<span className="inline-flex items-center gap-1">
									<code className="break-all">{traceId}</code>
									<Button
										variant="ghost"
										size="icon-xs"
										aria-label="Copy trace ID"
										onClick={() => {
											copy("trace", traceId);
										}}
									>
										{copied === "trace" ? <CheckIcon /> : <CopyIcon />}
									</Button>
								</span>
								{trace !== undefined && (
									<>
										<span>
											{spans.length} loaded{" "}
											{spans.length === 1 ? "span" : "spans"} · {trace.services}{" "}
											{trace.services === 1 ? "service" : "services"}
										</span>
										{trace.errors > 0 && (
											<span className="rounded-sm bg-bad-bg px-1.5 py-0.5 text-bad-fg">
												{trace.errors} error{" "}
												{trace.errors === 1 ? "span" : "spans"}
											</span>
										)}
									</>
								)}
							</div>
						</div>
						{trace !== undefined && (
							<div className="shrink-0 text-right">
								<p className="font-mono text-sm">
									{durationLabel(trace.duration)}
								</p>
								<p className="mt-0.5 text-[9px] text-fg3">Observed duration</p>
							</div>
						)}
					</div>
					{!validTrace && (
						<p id="trace-id-error" className="text-xs text-bad-fg">
							Enter a 32-character hexadecimal trace ID. Return to All traces to
							search again.
						</p>
					)}
					<TraceSurface>
						<QueryState
							loading={loading}
							error={query.error}
							empty={spans.length === 0}
							onRetry={() => {
								void query.refetch();
							}}
						/>
						{trace !== undefined && (
							<TraceWaterfall
								key={traceId}
								trace={trace}
								incomplete={incomplete}
							/>
						)}
						<PageStatus
							count={spans.length}
							incomplete={incomplete}
							fetching={query.isFetchingNextPage}
							onMore={() => {
								void query.fetchNextPage();
							}}
							detail
						/>
					</TraceSurface>
				</>
			) : (
				<>
					<h1 className="text-sm font-semibold">Traces</h1>
					<form
						noValidate
						className="flex flex-wrap items-center gap-1.5"
						onSubmit={(event) => {
							event.preventDefault();
							const id = input.trim().toLowerCase();
							if (id !== "" && !TRACE_ID_PATTERN.test(id)) {
								setInvalid(true);
								return;
							}
							update(id);
						}}
					>
						<div className="relative min-w-40 flex-1">
							<Input
								aria-label="Trace ID"
								aria-invalid={invalid}
								aria-describedby={invalid ? "trace-id-error" : undefined}
								value={input}
								placeholder="Find a trace by ID…"
								className="h-7 rounded-sm pr-7 font-mono text-[11px]"
								onChange={(event) => {
									setInput(event.target.value);
									setInvalid(false);
								}}
							/>
							{input !== "" && (
								<Button
									type="button"
									variant="ghost"
									size="icon-xs"
									aria-label="Clear trace search"
									className="absolute top-0 right-0 h-7 w-7"
									onClick={() => {
										update("");
										document
											.querySelector<HTMLInputElement>(
												'input[aria-label="Trace ID"]',
											)
											?.focus();
									}}
								>
									<XIcon />
								</Button>
							)}
						</div>
						<Button type="submit" variant="outline" size="xs">
							Search
						</Button>
						<ResourceMenu compact value={selected} onChange={setSelected} />
						{controls}
					</form>
					{invalid && (
						<p id="trace-id-error" className="text-xs text-bad-fg">
							Enter a 32-character hexadecimal trace ID.
						</p>
					)}
					<div className="flex flex-wrap justify-between gap-1 text-[10px] text-fg3">
						<span>
							{traces.length} {traces.length === 1 ? "trace" : "traces"} in{" "}
							{spans.length} loaded {spans.length === 1 ? "span" : "spans"}
							{incomplete ? " · Partial results" : ""}
						</span>
						<span>
							{formatClock(bounds.start)}–{formatClock(bounds.end)} · Oldest
							spans first
						</span>
					</div>
					<TraceSurface>
						{!filterReady && !resourcesLoading && (
							<div role="status" className="px-2.5 py-3 text-xs text-fg3">
								The selected resources are unavailable.{" "}
								<Button
									variant="outline"
									size="xs"
									onClick={() => {
										setSelected(null);
									}}
								>
									Show all resources
								</Button>
							</div>
						)}
						<QueryState
							loading={loading}
							error={query.error}
							empty={spans.length === 0 && filterReady}
							onRetry={() => {
								void query.refetch();
							}}
						/>
						{spans.length > 0 && (
							<table className="trace-results">
								<thead>
									<tr>
										<th className="w-[43%] max-sm:w-[56%]">
											Service / operation
										</th>
										<th className="w-[17%] max-sm:hidden">Trace ID</th>
										<th className="w-[20%] max-sm:w-[27%]">
											Observed duration
										</th>
										<th className="w-[10%] max-sm:w-[17%]">Spans</th>
										<th className="w-[10%] max-sm:hidden">Start</th>
									</tr>
								</thead>
								<tbody>
									{traces
										.slice(
											currentPage * ROWS_PER_PAGE,
											(currentPage + 1) * ROWS_PER_PAGE,
										)
										.map((item) => (
											<tr key={item.id}>
												<td>
													<button
														type="button"
														className="trace-results-operation w-full focus-visible:outline-2"
														aria-label={`Open trace ${item.id}`}
														onClick={() => {
															update(item.id);
														}}
														title={`${item.root.serviceName} · ${item.root.name}`}
													>
														<TraceDot
															color={traceServiceColor(item.root.serviceName)}
														/>
														<span className="max-w-28 truncate text-fg3 max-sm:hidden">
															{traceServiceLabel(
																item.root,
																resources.find(
																	(resource) =>
																		resource.id === item.root.resourceId,
																)?.name,
															)}
														</span>
														<strong>{item.root.name || "Unnamed span"}</strong>
														{item.errors > 0 && (
															<span
																title={`${String(item.errors)} error spans`}
																className="shrink-0 text-bad-fg"
															>
																!
															</span>
														)}
													</button>
												</td>
												<td className="font-mono text-fg3 max-sm:hidden">
													{item.id.slice(0, 12)}
												</td>
												<td>
													<span className="flex items-center gap-2">
														<span className="h-1.5 flex-1 rounded-sm bg-bg3 max-sm:hidden">
															<span
																className="block h-full rounded-sm"
																style={{
																	width: `${String(Number((item.duration * 100n) / maxDuration))}%`,
																	background: traceServiceColor(
																		item.root.serviceName,
																	),
																}}
															/>
														</span>
														<span className="w-20 shrink-0 text-right font-mono text-[10px] max-sm:w-auto">
															{durationLabel(item.duration)}
														</span>
													</span>
												</td>
												<td className="font-mono text-fg3">
													{item.spans.length}
												</td>
												<td className="font-mono text-[10px] text-fg3 max-sm:hidden">
													{formatClock(Number(item.start / 1_000_000n), true)}
												</td>
											</tr>
										))}
								</tbody>
							</table>
						)}
						{traces.length > ROWS_PER_PAGE && (
							<Pager
								className="border-t border-line"
								label={`Page ${String(currentPage + 1)} of ${String(pageCount)} loaded results`}
								onPrev={
									currentPage === 0
										? null
										: () => {
												setPage(currentPage - 1);
											}
								}
								onNext={
									currentPage === pageCount - 1
										? null
										: () => {
												setPage(currentPage + 1);
											}
								}
							/>
						)}
						<PageStatus
							count={spans.length}
							incomplete={incomplete}
							fetching={query.isFetchingNextPage}
							onMore={() => {
								void query.fetchNextPage();
							}}
						/>
					</TraceSurface>
				</>
			)}
		</div>
	);
}

function QueryState({
	loading,
	error,
	empty,
	onRetry,
}: {
	loading: boolean;
	error: Error | null;
	empty: boolean;
	onRetry: () => void;
}) {
	if (loading)
		return (
			<div
				role="status"
				className="flex min-h-[200px] items-center justify-center gap-2 text-fg3"
			>
				<RefreshCwIcon className="size-4 animate-spin motion-reduce:animate-none" />
				Loading spans…
			</div>
		);
	if (error !== null)
		return (
			<div
				role="alert"
				className="flex flex-wrap items-center justify-between gap-3 px-4 py-6"
			>
				<span className="text-bad-fg">
					{getErrorMessage(error, "Failed to load traces")}
				</span>
				<Button variant="outline" onClick={onRetry}>
					Retry
				</Button>
			</div>
		);
	if (empty)
		return (
			<EmptyState icon={<WaypointsIcon />} title="No traces in this time range">
				Choose a wider time range or send traces from your application through
				OTLP.
			</EmptyState>
		);
	return null;
}

function PageStatus({
	count,
	incomplete,
	fetching,
	onMore,
	detail = false,
}: {
	count: number;
	incomplete: boolean;
	fetching: boolean;
	onMore: () => void;
	detail?: boolean;
}) {
	if (count === 0) return null;
	return (
		<div className="flex flex-wrap items-center justify-between gap-2 border-t border-line bg-bg2 px-2.5 py-1.5 text-[10px] text-fg3">
			<span>
				{incomplete
					? "More spans are available. Counts and durations reflect loaded spans."
					: detail
						? "All available spans in this time range are loaded. Sampling or retention may leave gaps."
						: "All spans in this time range are loaded."}
			</span>
			{incomplete && (
				<Button
					variant="outline"
					size="sm"
					disabled={fetching}
					onClick={onMore}
				>
					{fetching ? "Loading…" : "Load more spans"}
				</Button>
			)}
		</div>
	);
}
