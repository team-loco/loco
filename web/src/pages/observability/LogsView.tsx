import { useListNavigation } from "@/hooks/useListNavigation";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { TimeAxis } from "@/components/design/TimeAxis";
import { createParser, parseAsString, useQueryStates } from "nuqs";
import { useEffect, useState, type CSSProperties } from "react";
import { queryLogBuckets, queryLogs } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { ArrowUpIcon } from "lucide-react";

import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { Button } from "@/components/design/Button";
import { ErrorState } from "@/components/design/ErrorState";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { useIsMobile } from "@/hooks/use-mobile";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useLogBuckets } from "@/hooks/useLogBuckets";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { useTailLogs } from "@/hooks/useTailLogs";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { pagingParsers } from "@/lib/paging";
import { createLogHistory, moveLogHistory, oldestLogHistory } from "@/lib/log-history";
import { msToTimestamp } from "@/lib/time";
import { msParser, windowLabel, windowSpan } from "@/lib/obs-time";
import { cn } from "@/lib/utils";

import { columnsParser, widthsParser, type ColumnKey } from "./columns";
import { useObs } from "./context";
import { fmtTs } from "./format";
import { LogDetail } from "./LogDetail";
import { cancelLogResume, completeLogResume, logPlaybackForKey, type LogPlayback } from "./log-playback";
import { LogTable } from "./LogTable";
import { coversWholeRange, CopyCommand, NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import {
	buildBackendQuery,
	LEVELS,
	queryError,
	queryStr,
	sameToken,
	searchWords,
	type FieldKey,
	type Level,
	type Token,
} from "./query";
import { QueryBar, QueryErrorLine } from "./QueryBar";
import { logVolume } from "./log-volume";
import { toRows, visibleLogRows, type LogRow } from "./rows";
import { PAGE_SIZES, VolumeBars, type VolumeBucket } from "./shared";
import { LiveButton } from "./Toolbar";
import { ViewHeader } from "./ViewHeader";

const BATCH = 1000;
const BUCKETS = 60;
const DEFAULT_PAGE_SIZE = 50;
const LOG_PAGE_SIZES = [...PAGE_SIZES, 250];
const LAST_PAGE = "last";
const { page: pageParser, size: sizeParser } = pagingParsers(LOG_PAGE_SIZES, DEFAULT_PAGE_SIZE);
const LOGS_PARAMS = {
	page: createParser<number | typeof LAST_PAGE>({
		parse: (raw) => (raw === LAST_PAGE ? LAST_PAGE : pageParser.parse(raw)),
		serialize: (page) => (page === LAST_PAGE ? LAST_PAGE : pageParser.serialize(page)),
	}).withDefault(0),
	size: sizeParser,
	log: parseAsString,
	cols: columnsParser,
	widths: widthsParser,
	at: msParser,
};
const TAIL_TICK_MS = 1000;
const IDLE_TICK_MS = 15_000;
const RATE_WINDOW_MS = 10_000;
const FRESH_MS = 1800;

const LEVEL_SERIES: Record<Level, { label: string; color: string }> = {
	error: { label: "Error", color: "var(--err)" },
	warn: { label: "Warning", color: "var(--lv-warn)" },
	info: { label: "Info", color: "var(--lv-info)" },
	debug: { label: "Debug", color: "var(--line2)" },
};

function levelToken(level: Level): Token {
	return { neg: false, key: "level", value: level };
}

function nearest(rows: LogRow[], ts: number): LogRow | undefined {
	let best: LogRow | undefined;
	for (const r of rows) {
		if (best === undefined || Math.abs(r.ts - ts) < Math.abs(best.ts - ts)) best = r;
	}
	return best;
}

export function LogsView() {
	const timePref = useTimePreferences();
	const { workspaceId, resources, scopedResourceIds, resourceById, maxMs, range, setRange, tokens, text, setQuery } =
		useObs();
	const [params, setParams] = useQueryStates(LOGS_PARAMS, { history: "replace" });
	const { active: environment, isLoading: environmentLoading } = useEnvironments();
	const environmentId = environment?.id ?? "";
	const mobile = useIsMobile();
	const { size, page, log: selectedKey, cols: columns, widths, at: anchor } = params;
	const [history, setHistory] = useState(createLogHistory);
	const [focusActive, setFocusActive] = useState(anchor !== null);
	const [windowEnd, setWindowEnd] = useState(Date.now);
	const [freezeTs, setFreezeTs] = useState<number | null>(null);
	const backend = buildBackendQuery(tokens, text, scopedResourceIds);
	const spanMs = windowSpan(range);
	const queryKey = JSON.stringify([
		workspaceId,
		environmentId,
		backend.query,
		backend.resourceIds,
		backend.impossible,
		range,
		anchor,
	]);
	const [prevKey, setPrevKey] = useState(queryKey);
	const cursor = history.current.cursor;
	const playbackKey = JSON.stringify([queryKey, cursor, history.current.order]);
	const [playback, setPlayback] = useState<LogPlayback>({ key: playbackKey, on: false, pending: null, paused: null });
	useEffect(() => {
		const cancel = () => {
			setPlayback(cancelLogResume);
		};
		window.addEventListener("popstate", cancel);
		return () => {
			window.removeEventListener("popstate", cancel);
		};
	}, []);
	const currentPlayback = logPlaybackForKey(playback, playbackKey);
	if (currentPlayback !== playback) setPlayback(currentPlayback);
	const canTail = anchor === null && range.kind === "relative";
	const tail = playback.on && canTail;
	const now = useNow(tail ? TAIL_TICK_MS : IDLE_TICK_MS);
	const nowMs = now.getTime();
	if (prevKey !== queryKey) {
		setPrevKey(queryKey);
		setHistory(createLogHistory());
		setWindowEnd(Math.max(windowEnd, nowMs));
		setFocusActive(anchor !== null);
		if (!canTail) setPlayback({ ...currentPlayback, on: false, pending: null, paused: null });
	}
	const historyFrom =
		range.kind === "absolute" ? range.from : anchor === null ? windowEnd - spanMs : anchor - spanMs / 2;
	const historyTo =
		range.kind === "absolute" ? range.to : anchor === null ? windowEnd : Math.min(windowEnd, anchor + spanMs / 2);

	const logs = useQueryLogs({
		workspaceId,
		environmentId,
		allEnvironments: backend.allEnvironments,
		resourceIds: backend.resourceIds,
		spanMs,
		query: backend.query,
		cursor,
		order: history.current.order,
		startTime: msToTimestamp(historyFrom),
		endTime: msToTimestamp(historyTo),
		around: anchor ?? undefined,
		limit: BATCH,
		enabled: !backend.impossible,
	});
	const tailState = useTailLogs({
		workspaceId,
		environmentId,
		allEnvironments: backend.allEnvironments,
		resourceIds: backend.resourceIds,
		query: backend.query,
		enabled: tail && cursor === "" && !backend.impossible,
	});

	const cutoffCursor = (playback.paused?.olderCursor ?? logs.olderCursor) || null;
	const newerCursor = (playback.paused?.newerCursor ?? logs.newerCursor) || null;
	const batchRows = toRows(logs.entries, resourceById).toSorted((a, b) => b.ts - a.ts);
	const allTail = tail && cursor === "" ? toRows(tailState.entries, resourceById) : [];

	const frozen = selectedKey !== null && freezeTs !== null;
	const tailRows = frozen ? allTail.filter((r) => r.ts <= freezeTs) : allTail;
	const held = frozen ? allTail.length - tailRows.length : 0;
	const rows = visibleLogRows(batchRows, tailRows, playback.paused?.rows ?? null);
	const rate = allTail.filter((r) => r.ts > nowMs - RATE_WINDOW_MS).length / (RATE_WINDOW_MS / 1000);
	// The page and selected line live in the URL so a reload or shared link reopens them.
	const setView = (nextPage: number | "last", key: string | null) => {
		void setParams({ page: nextPage, log: key });
	};
	const handleToggleTail = () => {
		setFreezeTs(null);
		if (tail) {
			setView(0, selectedKey);
			setPlayback({
				...playback,
				on: false,
				paused: {
					rows: visibleLogRows(batchRows, allTail),
					olderCursor: logs.olderCursor,
					newerCursor: logs.newerCursor,
				},
			});
			return;
		}
		const latest = logs.refetchLatest();
		const latestKey = JSON.stringify([
			workspaceId,
			environmentId,
			backend.query,
			backend.resourceIds,
			backend.impossible,
			range,
			null,
		]);
		const pending = { from: playbackKey, to: JSON.stringify([latestKey, "", LogOrder.NEWEST_FIRST]) };
		setHistory(createLogHistory());
		setWindowEnd(latest.end);
		setView(0, null);
		setPlayback({ ...playback, pending });
		void latest.result.then(
			() => {
				setPlayback((current) => completeLogResume(current, pending, true));
			},
			() => {
				setPlayback((current) => completeLogResume(current, pending, false));
			},
		);
	};

	const isLoading = (logs.isLoading || environmentLoading) && !backend.impossible;
	const truncated =
		cutoffCursor !== null || newerCursor !== null || history.older.length > 0 || history.newer.length > 0;
	const from = tail ? nowMs - spanMs : historyFrom;
	const to = tail ? nowMs : historyTo;

	const volumeEnd = tail ? Math.floor(to / IDLE_TICK_MS) * IDLE_TICK_MS : to;
	const volumeStart = tail ? volumeEnd - spanMs : from;
	const intervalSeconds = Math.max(1, Math.ceil((volumeEnd - volumeStart) / 1000 / (BUCKETS - 1)));
	const volume = useLogBuckets({
		workspaceId,
		environmentId,
		allEnvironments: backend.allEnvironments,
		resourceIds: backend.resourceIds,
		query: backend.query,
		start: volumeStart,
		end: volumeEnd,
		intervalSeconds,
		enabled: !backend.impossible,
	});
	const bw = intervalSeconds * 1000;
	const { points, totals } = logVolume(volume.data?.buckets ?? [], volumeStart, volumeEnd, bw);
	const chartStart = points[0]?.start ?? volumeStart;
	const chartEnd = chartStart + points.length * bw;
	const buckets: VolumeBucket[] = points.map((point) => {
		const total = [...point.counts.values()].reduce((a, n) => a + n, 0);
		return {
			label: `${fmtTs(point.start, false, timePref)}, ${formatCount(total)} ${total === 1 ? "line" : "lines"}`,
			segments: LEVELS.map((level) => ({ value: point.counts.get(level) ?? 0, color: LEVEL_SERIES[level].color })),
		};
	});

	const pages = Math.max(1, Math.ceil(rows.length / size));
	const focused = selectedKey === null && focusActive && anchor !== null ? nearest(rows, anchor) : undefined;
	const effectiveKey = selectedKey ?? focused?.key ?? null;
	const selIdx = effectiveKey !== null ? rows.findIndex((r) => r.key === effectiveKey) : -1;
	const selRow = selIdx >= 0 ? rows[selIdx] : undefined;
	const curPage =
		focused !== undefined && selIdx >= 0
			? Math.floor(selIdx / size)
			: page === "last"
				? pages - 1
				: Math.min(page, pages - 1);
	const pageRows = rows.slice(curPage * size, curPage * size + size);
	const canOlder = curPage < pages - 1 || history.older.length > 0 || cutoffCursor !== null;
	const canNewer = curPage > 0 || history.newer.length > 0 || newerCursor !== null;

	const select = (key: string | null) => {
		setFocusActive(false);
		if (key === null) {
			setFreezeTs(null);
			setView(curPage, null);
			return;
		}
		const idx = rows.findIndex((r) => r.key === key);
		setView(idx >= 0 ? Math.floor(idx / size) : curPage, key);
		if (freezeTs === null || selectedKey === null) setFreezeTs(rows[0]?.ts ?? nowMs);
	};
	useListNavigation(
		pageRows.map((row) => row.key),
		selRow?.key ?? null,
		select,
	);
	const toOlderBatch = () => {
		setPlayback({ ...playback, on: false, pending: null, paused: null });
		setHistory(moveLogHistory(history, "older", cutoffCursor ?? ""));
		setView(0, null);
	};
	const toNewerBatch = () => {
		setPlayback({ ...playback, on: false, pending: null, paused: null });
		setHistory(moveLogHistory(history, "newer", newerCursor ?? ""));
		setView("last", null);
	};
	const atNewest = curPage === 0 && history.newer.length === 0 && newerCursor === null;
	const atOldest = curPage === pages - 1 && history.older.length === 0 && cutoffCursor === null;

	const valueCounts = (key: FieldKey): [string, number][] => {
		const m = new Map<string, number>();
		const add = (v: string, c = 1) => {
			if (v !== "") m.set(v, (m.get(v) ?? 0) + c);
		};
		if (key === "level")
			LEVELS.forEach((l) => {
				add(l, 0);
			});
		if (key === "resource")
			resources.forEach((r) => {
				add(r.name, 0);
			});
		for (const r of rows) {
			if (key === "level") add(r.level);
			if (key === "resource") add(r.resourceName);
			if (key === "region") add(r.region);
			if (key === "replica") add(r.pod);
			if (key === "build") add(r.build);
			if (key === "trace") add(r.entry.traceId);
		}
		return [...m.entries()].toSorted((a, b) => b[1] - a[1]);
	};

	const panelOpen = selRow !== undefined;
	const freeWords = searchWords(text);
	const error = logs.error ?? (tail ? tailState.error : null);
	const query = queryStr(tokens, text);
	const setColumns = (next: ColumnKey[]) => {
		void setParams({ cols: next });
	};
	const toggleLevel = (level: Level) => {
		const tok = levelToken(level);
		const on = tokens.some((t) => sameToken(t, tok));
		setQuery(on ? tokens.filter((t) => !sameToken(t, tok)) : [...tokens, tok], text);
	};
	const pressedLevels = LEVELS.filter((l) => tokens.some((t) => sameToken(t, levelToken(l))));
	const away = anchor !== null || range.kind === "absolute";

	const returnToLatest = () => {
		setRange({ kind: "relative", spanMs });
	};
	const header = (
		<ViewHeader
			actions={
				<>
					{away && (
						<Button variant="ghost" onClick={returnToLatest}>
							Return to latest
						</Button>
					)}
					<LiveButton
						on={tail}
						pending={playback.pending !== null}
						disabled={!canTail || cursor !== "" || backend.impossible}
						detail={`${rate.toFixed(1)}/s`}
						title={
							canTail ? (tail ? "Pause live tail" : "Stream new logs as they arrive") : "Return to latest to go live"
						}
						onToggle={handleToggleTail}
					/>
				</>
			}
		/>
	);

	if (!isLoading && error !== null && isUnimplemented(error)) {
		return (
			<>
				{header}
				<ObsHero
					view="logs"
					title="Logs aren't available here yet"
					body="This installation has no log store connected. Anything your resources print to stdout or stderr shows up here once it does."
				/>
			</>
		);
	}

	const rejected = error === null ? null : queryError(error, backend.query);
	if (rejected !== null) {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<QueryErrorLine error={rejected} />
			</>
		);
	}

	if (!isLoading && logs.error !== null) {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ErrorState error={logs.error} what="logs" rpc={queryLogs} onRetry={logs.refetch} />
			</>
		);
	}

	const firstRun = !isLoading && error === null && rows.length === 0 && !tail && query === "";

	return (
		<>
			{header}
			{firstRun ? (
				<ObsHero
					view="logs"
					title={
						anchor !== null
							? "No logs around this time"
							: coversWholeRange(range, maxMs)
								? "No logs collected"
								: `No logs in the ${windowLabel(range, timePref).toLowerCase()}`
					}
					body="Anything your resources write to stdout or stderr lands here."
					action={
						<>
							<CopyCommand command={`loco resource logs ${resources[0]?.name ?? "<name>"} --follow`} />
							<WidenRangeButton />
						</>
					}
				/>
			) : (
				<>
					<QueryBar valueCounts={valueCounts} />
					<section aria-label="Log volume" className="flex flex-col gap-2.5 rounded-card bg-panel px-5 pt-4 pb-3.5">
						<ToggleGroup
							multiple
							aria-label="Log levels"
							data-log-levels
							className="flex flex-wrap gap-1"
							value={pressedLevels}
							onValueChange={(values) => {
								const level = LEVELS.find((l) => values.includes(l) !== pressedLevels.includes(l));
								if (level !== undefined) toggleLevel(level);
							}}
						>
							{LEVELS.map((l) => {
								const n = totals.get(l) ?? 0;
								return (
									<ToggleGroupItem
										key={l}
										value={l}
										className={cn(
											"inline-flex h-[26px] items-center gap-1.5 rounded-sm px-2 text-[12.5px] text-fg2 hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none aria-pressed:bg-background aria-pressed:text-foreground aria-pressed:shadow-[inset_0_0_0_1px_var(--line2)]",
											n === 0 && "opacity-55 aria-pressed:opacity-100",
										)}
									>
										<span className="size-2 rounded-[2px]" style={{ background: LEVEL_SERIES[l].color }} />
										{LEVEL_SERIES[l].label}
										<span className="font-mono text-[11.5px] text-fg3">
											{volume.data === undefined && !backend.impossible ? "—" : formatCount(n)}
										</span>
									</ToggleGroupItem>
								);
							})}
						</ToggleGroup>
						{volume.error !== null ? (
							<ErrorState error={volume.error} what="log volume" rpc={queryLogBuckets} onRetry={volume.refetch} />
						) : volume.isLoading ? (
							<Skeleton className="h-14 w-full" />
						) : (
							<>
								<VolumeBars
									buckets={buckets}
									label="Log volume over the selected range. Arrow keys move between bars, Enter zooms in."
									onPick={(i) => {
										setRange({ kind: "absolute", from: chartStart + i * bw, to: chartStart + (i + 1) * bw });
									}}
								/>
								<TimeAxis from={chartStart} to={chartEnd} />
							</>
						)}
					</section>

					<div
						className="grid grid-cols-1 items-start gap-4 xl:grid-cols-[var(--log-columns)]"
						style={
							{
								"--log-columns": panelOpen ? "minmax(0,1fr) minmax(380px,440px)" : "minmax(0,1fr)",
							} as CSSProperties
						}
					>
						<section aria-label="Log lines" className="flex min-w-0 flex-col rounded-card bg-panel">
							{held > 0 && (
								<button
									type="button"
									onClick={() => {
										setFreezeTs(allTail[0]?.ts ?? nowMs);
										setView(0, selectedKey);
									}}
									className="flex h-[30px] w-full items-center justify-center gap-1.5 rounded-t-card bg-ok-bg font-medium text-ok-fg hover:brightness-[0.97]"
								>
									<ArrowUpIcon className="size-[13px]" />
									{held} {held === 1 ? "new line" : "new lines"}
								</button>
							)}
							<div className="overflow-x-auto px-5 pt-1 pb-3 [scrollbar-width:thin]">
								<LogTable
									rows={pageRows}
									columns={columns}
									compact={panelOpen || mobile}
									selectedKey={selRow?.key ?? null}
									words={freeWords}
									highlightAfter={tail ? nowMs - FRESH_MS : null}
									onSelect={(key) => {
										select(key === selRow?.key ? null : key);
									}}
									onColumns={setColumns}
									widths={widths}
									onWidths={(next) => {
										void setParams({ widths: next });
									}}
								/>
								{isLoading && <LoadingRows />}
								{!isLoading && error !== null && (
									<div role="alert" className="px-3 py-6 break-words text-bad-fg">
										{getErrorMessage(error, "Failed to load logs")}
									</div>
								)}
								{!isLoading && error === null && rows.length === 0 && tail && (
									<div className="px-3 py-6 text-fg3">Waiting for new logs…</div>
								)}
								{!isLoading && error === null && rows.length === 0 && !tail && query !== "" && (
									<NoMatches title="No lines match these filters" />
								)}
							</div>
							<Pager
								label={
									rows.length > 0
										? `${formatCount(curPage * size + 1)}–${formatCount(Math.min(rows.length, (curPage + 1) * size))} of ${formatCount(rows.length)}${truncated ? "+" : ""} loaded`
										: "0"
								}
								page={curPage}
								pages={pages}
								more={truncated}
								total={rows.length}
								pageSizes={LOG_PAGE_SIZES}
								pageSize={size}
								onPageSize={(n) => {
									void setParams({ size: n, page: null });
								}}
								prevLabel="Newer"
								nextLabel="Older"
								firstLabel="Newest"
								lastLabel="Oldest"
								onFirst={
									atNewest
										? null
										: () => {
												setFocusActive(false);
												setPlayback({ ...playback, on: false, pending: null, paused: null });
												if (anchor === null) setHistory(createLogHistory());
												setView(0, null);
											}
								}
								onPrev={
									canNewer
										? () => {
												setFocusActive(false);
												if (curPage > 0) setView(curPage - 1, selectedKey);
												else toNewerBatch();
											}
										: null
								}
								onNext={
									canOlder
										? () => {
												setFocusActive(false);
												if (curPage < pages - 1) setView(curPage + 1, selectedKey);
												else toOlderBatch();
											}
										: null
								}
								onLast={
									atOldest || anchor !== null || tail
										? null
										: () => {
												setFocusActive(false);
												setPlayback({ ...playback, on: false, pending: null, paused: null });
												// Lines past this batch are reachable only through cursors, so jump to the oldest batch.
												if (cutoffCursor !== null || history.older.length > 0) setHistory(oldestLogHistory());
												setView("last", null);
											}
								}
							/>
						</section>
						{selRow !== undefined && (
							<LogDetail
								key={selRow.key}
								row={selRow}
								words={freeWords}
								columns={columns}
								onColumns={setColumns}
								onPrev={
									selIdx > 0
										? () => {
												select(rows[selIdx - 1]?.key ?? null);
											}
										: null
								}
								onNext={
									selIdx < rows.length - 1
										? () => {
												select(rows[selIdx + 1]?.key ?? null);
											}
										: null
								}
								onClose={() => {
									select(null);
								}}
							/>
						)}
					</div>
				</>
			)}
		</>
	);
}

function LoadingRows() {
	return (
		<div className="flex flex-col gap-0.5 pt-1">
			{Array.from({ length: 10 }, (_, i) => (
				<div key={String(i)} className="flex h-[34px] items-center gap-3 px-3">
					<Skeleton className="h-3 w-[120px]" />
					<Skeleton className="h-3 w-12" />
					<Skeleton className="h-3 w-[100px]" />
					<Skeleton className={cn("h-3", i % 2 === 0 ? "w-1/2" : "w-1/3")} />
				</div>
			))}
		</div>
	);
}
