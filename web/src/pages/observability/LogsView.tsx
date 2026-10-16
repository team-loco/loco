import { useState, type CSSProperties } from "react";
import { useSearchParams } from "react-router";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { ArrowUpIcon, PanelLeftCloseIcon, PanelLeftOpenIcon, ScrollTextIcon, SearchXIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { useNow } from "@/hooks/useNow";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { useTailLogs } from "@/hooks/useTailLogs";
import { getErrorMessage } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { createLogHistory, logAnchor, moveLogHistory } from "@/lib/log-history";
import { timeRangeMs } from "@/lib/obs";
import { msToTimestamp } from "@/lib/time";
import { cn } from "@/lib/utils";

import { QUERY_MAX_RANGE, RANGES, useObs } from "./context";
import { fmtTs } from "./format";
import { LogDetail } from "./LogDetail";
import { completeLogResume, logPlaybackForKey, type LogPlayback } from "./log-playback";
import { LogFacets } from "./LogFacets";
import { LogTable } from "./LogTable";
import { buildBackendQuery, LEVELS, queryStr, type FieldKey } from "./query";
import { QueryBar } from "./QueryBar";
import { toRows, visibleLogRows, type LogRow } from "./rows";
import { Histogram, PAGE_SIZES, type HistoBucket } from "./shared";
import { WidenRangeButton } from "./ObsEmpty";
import { LiveTailButton, TimeRangeMenu } from "./Toolbar";

const BATCH = 1000;
const WHITESPACE = /\s+/;
const BUCKETS = 48;

function nearest(rows: LogRow[], ts: number): LogRow | undefined {
	let best: LogRow | undefined;
	for (const r of rows) {
		if (best === undefined || Math.abs(r.ts - ts) < Math.abs(best.ts - ts)) best = r;
	}
	return best;
}

export function LogsView() {
	const {
		workspaceId,
		resources,
		resourceById,
		range,
		tokens,
		setTokens,
		setText,
		appliedText,
		setAppliedText,
	} = useObs();
	const [facetsOpen, setFacetsOpen] = useState(true);
	const [size, setSize] = useState(50);
	const [page, setPage] = useState<number | "last">(0);
	const [history, setHistory] = useState(createLogHistory);
	const [params, setParams] = useSearchParams();
	const anchor = logAnchor(params.get("at"));
	const [focusActive, setFocusActive] = useState(anchor !== null);
	const clearAnchor = () => {
		const next = new URLSearchParams(params);
		next.delete("at");
		setParams(next);
		setFocusActive(false);
	};
	const [windowEnd, setWindowEnd] = useState(Date.now);
	const [selectedKey, setSelectedKey] = useState<string | null>(null);
	const [freezeTs, setFreezeTs] = useState<number | null>(null);
	const backend = buildBackendQuery(tokens, appliedText, resources);
	const queryKey = JSON.stringify([workspaceId, backend.parsed, backend.resourceIds, backend.impossible, range, anchor]);
	const [prevKey, setPrevKey] = useState(queryKey);
	const cursor = history.current.cursor;
	const playbackKey = JSON.stringify([queryKey, cursor, history.current.order]);
	const [playback, setPlayback] = useState<LogPlayback>({ key: playbackKey, on: false, pending: null, paused: null });
	const currentPlayback = logPlaybackForKey(playback, playbackKey);
	if (currentPlayback !== playback) setPlayback(currentPlayback);
	const tail = playback.on;
	const now = useNow(tail ? 1000 : 15_000);
	const nowMs = now.getTime();
	if (prevKey !== queryKey) {
		setPrevKey(queryKey);
		setHistory(createLogHistory());
		setWindowEnd(Math.max(windowEnd, nowMs));
		setFocusActive(anchor !== null);
		setPage(0);
		setSelectedKey(null);
	}
	const historyFrom = anchor === null ? windowEnd - timeRangeMs(range) : anchor - timeRangeMs(range) / 2;
	const historyTo = anchor === null ? windowEnd : Math.min(windowEnd, anchor + timeRangeMs(range) / 2);

	const logs = useQueryLogs({
		workspaceId,
		resourceIds: backend.resourceIds,
		timeRange: range,
		parsedQuery: backend.parsed,
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
		resourceIds: backend.resourceIds,
		parsedQuery: backend.parsed,
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
	const rate = allTail.filter((r) => r.ts > nowMs - 10_000).length / 10;
	const handleToggleTail = () => {
		setPage(0);
		setFreezeTs(null);
		if (tail) {
			setPlayback({ ...playback, on: false, paused: { rows: visibleLogRows(batchRows, allTail), olderCursor: logs.olderCursor, newerCursor: logs.newerCursor } });
			return;
		}
		const latest = logs.refetchLatest();
		const latestKey = JSON.stringify([workspaceId, backend.parsed, backend.resourceIds, backend.impossible, range, null]);
		const pending = { from: playbackKey, to: JSON.stringify([latestKey, "", LogOrder.NEWEST_FIRST]) };
		clearAnchor();
		setHistory(createLogHistory());
		setWindowEnd(latest.end);
		setSelectedKey(null);
		setPlayback({ ...playback, pending });
		void latest.result.then(() => {
			setPlayback((current) => completeLogResume(current, pending, true));
		}, () => {
			setPlayback((current) => completeLogResume(current, pending, false));
		});
	};

	const isLoading = logs.isLoading && !backend.impossible;
	const truncated = cutoffCursor !== null || newerCursor !== null || history.older.length > 0 || history.newer.length > 0;
	const from = tail ? nowMs - timeRangeMs(range) : historyFrom;

	const bw = ((tail ? nowMs : historyTo) - from) / BUCKETS;
	const counts = Array.from({ length: BUCKETS }, () => ({ e: 0, w: 0, i: 0 }));
	for (const r of rows) {
		const idx = Math.min(BUCKETS - 1, Math.floor((r.ts - from) / bw));
		const b = counts[idx];
		if (idx < 0 || b === undefined) continue;
		if (r.level === "error") b.e++;
		else if (r.level === "warn") b.w++;
		else b.i++;
	}
	const histo: HistoBucket[] = counts.map((b, i) => ({
		title: `${fmtTs(from + i * bw)} · ${String(b.e + b.w + b.i)} lines (${String(b.e)} error, ${String(b.w)} warn)`,
		segments: [
			{ value: b.e, className: "bg-err" },
			{ value: b.w, className: "bg-warn" },
			{ value: b.i, className: "bg-info" },
		],
	}));

	const pages = Math.max(1, Math.ceil(rows.length / size));
	const focused = selectedKey === null && focusActive && anchor !== null ? nearest(rows, anchor) : undefined;
	const effectiveKey = selectedKey ?? focused?.key ?? null;
	const selIdx = effectiveKey !== null ? rows.findIndex((r) => r.key === effectiveKey) : -1;
	const selRow = selIdx >= 0 ? rows[selIdx] : undefined;
	const curPage = focused !== undefined && selIdx >= 0 ? Math.floor(selIdx / size) : page === "last" ? pages - 1 : Math.min(page, pages - 1);
	const pageRows = rows.slice(curPage * size, curPage * size + size);
	const canOlder = curPage < pages - 1 || history.older.length > 0 || cutoffCursor !== null;
	const canNewer = curPage > 0 || history.newer.length > 0 || newerCursor !== null;

	const select = (key: string | null) => {
		setFocusActive(false);
		setSelectedKey(key);
		if (key === null) {
			setFreezeTs(null);
			return;
		}
		const idx = rows.findIndex((r) => r.key === key);
		if (idx >= 0) setPage(Math.floor(idx / size));
		if (freezeTs === null || selectedKey === null) setFreezeTs(rows[0]?.ts ?? nowMs);
	};

	const valueCounts = (key: FieldKey): [string, number][] => {
		const m = new Map<string, number>();
		const add = (v: string, c = 1) => {
			if (v !== "") m.set(v, (m.get(v) ?? 0) + c);
		};
		if (key === "level") LEVELS.forEach((l) => { add(l, 0); });
		if (key === "resource") resources.forEach((r) => { add(r.name, 0); });
		for (const r of rows) {
			if (key === "level") add(r.level);
			if (key === "resource") add(r.resourceName);
			if (key === "region") add(r.region);
			if (key === "replica") add(r.pod);
		}
		return [...m.entries()].toSorted((a, b) => b[1] - a[1]);
	};

	const panelOpen = selRow !== undefined;
	const showFacets = facetsOpen && !panelOpen;
	const freeWords = appliedText.split(WHITESPACE).filter(Boolean);
	const rangeLabel = RANGES.find((r) => r.key === range)?.key ?? range;
	const matched = `${formatCount(rows.length)}${truncated ? "+" : ""} loaded lines`;
	const error = logs.error ?? (tail ? tailState.error : null);
	const query = queryStr(tokens, appliedText);
	const widen = range === QUERY_MAX_RANGE ? undefined : <WidenRangeButton max={QUERY_MAX_RANGE} />;
	const clearSearch = () => {
		setTokens([]);
		setText("");
		setAppliedText("");
	};

	return (
		<>
			<div className="flex flex-wrap items-center gap-2">
				<QueryBar valueCounts={valueCounts} />
				<TimeRangeMenu maxRange={QUERY_MAX_RANGE} mode={anchor === null ? "relative" : "window"} />
				<LiveTailButton
					on={tail}
					rate={rate}
					pending={playback.pending !== null}
					disabled={cursor !== "" || backend.impossible}
					onToggle={handleToggleTail}
				/>
			</div>
			<div
				className="grid grid-cols-1 items-start gap-5 lg:grid-cols-[var(--log-columns)]"
				style={{
					"--log-columns": [showFacets ? "200px" : null, "minmax(0,1fr)", panelOpen ? "minmax(360px,420px)" : null]
						.filter(Boolean)
						.join(" "),
				} as CSSProperties}
			>
				{showFacets && <LogFacets rows={rows} />}
				<div className="flex min-w-0 flex-col gap-4">
					<section className="rounded-lg border border-line bg-background">
						<div className="flex flex-wrap items-center gap-2.5 border-b border-line px-4 py-2.5">
							<Button
								variant="ghost"
								size="icon-sm"
								title={facetsOpen ? "Hide filters" : "Show filters"}
								onClick={() => {
									setFacetsOpen(!facetsOpen);
								}}
								className="-ml-1.5 text-fg3 hover:text-foreground"
							>
								{facetsOpen ? <PanelLeftCloseIcon className="size-[15px]" /> : <PanelLeftOpenIcon className="size-[15px]" />}
							</Button>
							{isLoading ? <Skeleton className="h-4 w-20" /> : <span className="font-semibold">{matched}</span>}
							<span className="text-fg3">{anchor === null ? `last ${rangeLabel}` : `around ${fmtTs(anchor, true)}`}</span>
							{anchor !== null && <Button variant="ghost" size="xs" onClick={() => {
								clearAnchor();
								setWindowEnd(Date.now());
								setSelectedKey(null);
							}}>Return to latest</Button>}
							<div className="flex-1" />
							<span className="flex items-center gap-3 text-sm text-fg3">
								<span className="flex items-center gap-[5px]">
									<span className="size-2 rounded-[2px] bg-err" />
									Error
								</span>
								<span className="flex items-center gap-[5px]">
									<span className="size-2 rounded-[2px] bg-warn" />
									Warn
								</span>
								<span className="flex items-center gap-[5px]">
									<span className="size-2 rounded-[2px] bg-info" />
									Info · Debug
								</span>
							</span>
						</div>
						<Histogram buckets={histo} from={fmtTs(from)} to={anchor === null ? "now" : fmtTs(historyTo)} height={84} />
					</section>

					<section className="overflow-hidden rounded-lg border border-line bg-background">
						{held > 0 && (
							<button
								type="button"
								onClick={() => {
									setFreezeTs(allTail[0]?.ts ?? nowMs);
									setPage(0);
								}}
								className="flex h-[30px] w-full cursor-pointer items-center justify-center gap-1.5 border-b border-line bg-ok-bg font-medium text-ok-fg hover:brightness-[0.97]"
							>
								<ArrowUpIcon className="size-[13px]" />
								{held} {held === 1 ? "new line" : "new lines"}
							</button>
						)}
						<div className="overflow-x-auto">
							<div className="min-w-[640px]">
								<LogTable
									rows={pageRows}
									compact={panelOpen}
									selectedKey={selRow?.key ?? null}
									words={freeWords}
									highlightAfter={tail ? nowMs - 1800 : null}
									onSelect={(key) => {
										select(key === selRow?.key ? null : key);
									}}
								/>
								{isLoading && <LoadingRows />}
								{!isLoading && error !== null && (
									<div className="px-4 py-6 break-words text-bad-fg">{getErrorMessage(error, "Failed to load logs")}</div>
								)}
								{!isLoading && error === null && rows.length === 0 && tail && (
									<div className="px-4 py-6 text-fg3">Waiting for new logs…</div>
								)}
								{!isLoading && error === null && rows.length === 0 && !tail && (
									<div className="border-b border-line">
										{query === "" ? (
											<EmptyState
												icon={<ScrollTextIcon />}
												title={anchor === null ? `No logs in the last ${range}` : `No logs around ${fmtTs(anchor)}`}
												action={widen}
											/>
										) : (
											<EmptyState
												icon={<SearchXIcon />}
												title="No logs match"
												query={query}
												action={
													<>
														<Button variant="outline" onClick={clearSearch}>
															<XIcon />
															Clear search
														</Button>
														{widen}
													</>
												}
											/>
										)}
									</div>
								)}
							</div>
						</div>
						<Pager
							className="bg-bg2"
							label={
								rows.length > 0
									? `${formatCount(curPage * size + 1)}–${formatCount(Math.min(rows.length, (curPage + 1) * size))} of ${formatCount(rows.length)}${truncated ? "+" : ""}`
									: "0"
							}
							pageSizes={PAGE_SIZES}
							pageSize={size}
							onPageSize={(n) => {
								setSize(n);
								setPage(0);
							}}
							prevLabel="Newer"
							nextLabel="Older"
							onPrev={
								canNewer
									? () => {
											setFocusActive(false);
											if (curPage > 0) setPage(curPage - 1);
											else {
												setPlayback({ ...playback, on: false, pending: null, paused: null });
												setHistory(moveLogHistory(history, "newer", newerCursor ?? ""));
												setPage("last");
											}
										}
									: null
							}
							onNext={
								canOlder
									? () => {
											setFocusActive(false);
											if (curPage < pages - 1) setPage(curPage + 1);
											else {
												setPlayback({ ...playback, on: false, pending: null, paused: null });
												setHistory(moveLogHistory(history, "older", cutoffCursor ?? ""));
												setPage(0);
											}
										}
									: null
							}
						/>
					</section>
				</div>
				{selRow !== undefined && (
					<LogDetail
						key={selRow.key}
						row={selRow}
						words={freeWords}
						nowMs={nowMs}
						onPrev={selIdx > 0 ? () => { select(rows[selIdx - 1]?.key ?? null); } : null}
						onNext={selIdx < rows.length - 1 ? () => { select(rows[selIdx + 1]?.key ?? null); } : null}
						onClose={() => {
							select(null);
						}}
					/>
				)}
			</div>
		</>
	);
}

function LoadingRows() {
	return (
		<div className="flex flex-col">
			{Array.from({ length: 10 }, (_, i) => (
				<div key={String(i)} className="flex h-[30px] items-center gap-3 border-b border-line px-4">
					<Skeleton className="h-3 w-[130px]" />
					<Skeleton className="h-[18px] w-12" />
					<Skeleton className="h-3 w-[100px]" />
					<Skeleton className={cn("h-3", i % 2 === 0 ? "w-1/2" : "w-1/3")} />
				</div>
			))}
		</div>
	);
}
