import { useEffect, useState, type CSSProperties } from "react";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { ArrowUpIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useIsMobile } from "@/hooks/use-mobile";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { useTailLogs } from "@/hooks/useTailLogs";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { createLogHistory, logAnchor, moveLogHistory } from "@/lib/log-history";
import { formatHourMinute, msToTimestamp } from "@/lib/time";
import { windowLabel, windowSpan } from "@/lib/obs-time";
import { cn } from "@/lib/utils";

import { readColumns, writeColumns, type ColumnKey } from "./columns";
import { useObs } from "./context";
import { fmtTs } from "./format";
import { LogDetail } from "./LogDetail";
import { cancelLogResume, completeLogResume, logPlaybackForKey, type LogPlayback } from "./log-playback";
import { LogTable } from "./LogTable";
import { NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import { buildBackendQuery, LEVELS, queryStr, sameToken, type FieldKey, type Level } from "./query";
import { QueryBar } from "./QueryBar";
import { toRows, visibleLogRows, type LogRow } from "./rows";
import { PAGE_SIZES, readPaging, VolumeBars, type VolumeBucket } from "./shared";
import { LiveButton } from "./Toolbar";
import { ViewHeader } from "./ViewHeader";

const BATCH = 1000;
const WHITESPACE = /\s+/;
const BUCKETS = 60;
const DEFAULT_PAGE_SIZE = 50;
const TAIL_TICK_MS = 1000;
const IDLE_TICK_MS = 15_000;
const RATE_WINDOW_MS = 10_000;
const FRESH_MS = 1800;

type ColorBy = "level" | "resource";

const LEVEL_SERIES: Record<Level, { label: string; color: string }> = {
	error: { label: "Error", color: "var(--err)" },
	warn: { label: "Warning", color: "var(--warn)" },
	info: { label: "Info", color: "color-mix(in oklab, var(--fg) 28%, var(--panel))" },
	debug: { label: "Debug", color: "var(--line2)" },
};

function nearest(rows: LogRow[], ts: number): LogRow | undefined {
	let best: LogRow | undefined;
	for (const r of rows) {
		if (best === undefined || Math.abs(r.ts - ts) < Math.abs(best.ts - ts)) best = r;
	}
	return best;
}

export function LogsView() {
	const { workspaceId, resources, resourceById, range, setRange, tokens, text, setQuery, params, setParam } = useObs();
	const { active: environment, isLoading: environmentLoading } = useEnvironments();
	const environmentId = environment?.id ?? "";
	const mobile = useIsMobile();
	const { size } = readPaging(params, DEFAULT_PAGE_SIZE);
	const columns = readColumns(params.get("cols"));
	const colorBy: ColorBy = params.get("color") === "resource" ? "resource" : "level";
	const [page, setPage] = useState<number | "last">(0);
	const [history, setHistory] = useState(createLogHistory);
	const anchor = logAnchor(params.get("at"));
	const [focusActive, setFocusActive] = useState(anchor !== null);
	const [windowEnd, setWindowEnd] = useState(Date.now);
	const [selectedKey, setSelectedKey] = useState<string | null>(null);
	const [freezeTs, setFreezeTs] = useState<number | null>(null);
	const backend = buildBackendQuery(tokens, text, resources);
	const spanMs = windowSpan(range);
	const queryKey = JSON.stringify([
		workspaceId,
		environmentId,
		backend.parsed,
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
		setPage(0);
		setSelectedKey(null);
		if (!canTail) setPlayback({ ...currentPlayback, on: false, pending: null, paused: null });
	}
	const historyFrom =
		range.kind === "absolute" ? range.from : anchor === null ? windowEnd - spanMs : anchor - spanMs / 2;
	const historyTo =
		range.kind === "absolute" ? range.to : anchor === null ? windowEnd : Math.min(windowEnd, anchor + spanMs / 2);

	const logs = useQueryLogs({
		workspaceId,
		environmentId,
		resourceIds: backend.resourceIds,
		spanMs,
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
		environmentId,
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
	const rate = allTail.filter((r) => r.ts > nowMs - RATE_WINDOW_MS).length / (RATE_WINDOW_MS / 1000);
	const handleToggleTail = () => {
		setPage(0);
		setFreezeTs(null);
		if (tail) {
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
			backend.parsed,
			backend.resourceIds,
			backend.impossible,
			range,
			null,
		]);
		const pending = { from: playbackKey, to: JSON.stringify([latestKey, "", LogOrder.NEWEST_FIRST]) };
		setHistory(createLogHistory());
		setWindowEnd(latest.end);
		setSelectedKey(null);
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

	const bw = (to - from) / BUCKETS;
	const groups =
		colorBy === "level"
			? LEVELS.map((l) => ({ id: l, label: LEVEL_SERIES[l].label, color: LEVEL_SERIES[l].color }))
			: resources.map((r) => ({ id: r.name, label: r.name, color: r.color }));
	const groupOf = (r: LogRow) => (colorBy === "level" ? r.level : r.resourceName);
	const counts = Array.from({ length: BUCKETS }, () => new Map<string, number>());
	const totals = new Map<string, number>();
	for (const r of rows) {
		const idx = Math.min(BUCKETS - 1, Math.floor((r.ts - from) / bw));
		const bucket = counts[idx];
		const g = groupOf(r);
		totals.set(g, (totals.get(g) ?? 0) + 1);
		if (idx < 0 || bucket === undefined) continue;
		bucket.set(g, (bucket.get(g) ?? 0) + 1);
	}
	const buckets: VolumeBucket[] = counts.map((b, i) => {
		const total = [...b.values()].reduce((a, n) => a + n, 0);
		return {
			label: `${fmtTs(from + i * bw)}, ${formatCount(total)} loaded ${total === 1 ? "line" : "lines"}`,
			segments: groups.map((g) => ({ value: b.get(g.id) ?? 0, color: g.color })),
		};
	});
	const legendKey: FieldKey = colorBy === "level" ? "level" : "resource";

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
		}
		return [...m.entries()].toSorted((a, b) => b[1] - a[1]);
	};

	const panelOpen = selRow !== undefined;
	const freeWords = text.split(WHITESPACE).filter(Boolean);
	const error = logs.error ?? (tail ? tailState.error : null);
	const query = queryStr(tokens, text);
	const setColumns = (next: ColumnKey[]) => {
		setParam("cols", writeColumns(next), true);
	};
	const toggleLegend = (id: string) => {
		const tok = { neg: false, key: legendKey, value: id };
		const on = tokens.some((t) => sameToken(t, tok));
		setQuery(on ? tokens.filter((t) => !sameToken(t, tok)) : [...tokens, tok], text);
	};
	const away = anchor !== null || range.kind === "absolute";
	const total = tail ? null : logs.totalMatched;
	const countLabel =
		total !== null && total >= rows.length
			? `${formatCount(total)} matching ${total === 1 ? "line" : "lines"}`
			: `${formatCount(rows.length)}${truncated ? "+" : ""} ${rows.length === 1 ? "line" : "lines"}`;

	const returnToLatest = () => {
		setRange({ kind: "relative", spanMs });
		setSelectedKey(null);
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

	const firstRun = !isLoading && error === null && rows.length === 0 && !tail && query === "";

	return (
		<>
			{header}
			<QueryBar valueCounts={valueCounts} />
			{firstRun ? (
				<ObsHero
					view="logs"
					title={anchor === null ? `No logs in the ${windowLabel(range).toLowerCase()}` : "No logs around this time"}
					body="Anything your resources print to stdout or stderr shows up here, searchable."
					action={<WidenRangeButton />}
				/>
			) : (
				<>
					<section aria-label="Log volume" className="flex flex-col gap-2.5 rounded-card bg-panel px-5 pt-4 pb-3.5">
						<div className="flex flex-wrap items-center gap-x-4 gap-y-2">
							{isLoading ? (
								<Skeleton className="h-4 w-28" />
							) : (
								<span
									className="text-[13.5px]"
									title={total !== null ? "Approximate count from the log store" : undefined}
								>
									{countLabel}
								</span>
							)}
							<div className="mr-auto flex flex-wrap gap-1">
								{groups.map((g) => {
									const n = totals.get(g.id) ?? 0;
									const on = tokens.some((t) => sameToken(t, { neg: false, key: legendKey, value: g.id }));
									return (
										<button
											key={g.id}
											type="button"
											aria-pressed={on}
											onClick={() => {
												toggleLegend(g.id);
											}}
											className={cn(
												"inline-flex h-[26px] items-center gap-1.5 rounded-sm px-2 text-[12.5px] text-fg2 hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
												on && "bg-background text-foreground shadow-[inset_0_0_0_1px_var(--line2)]",
												n === 0 && !on && "opacity-55",
											)}
										>
											<span className="size-2 rounded-[2px]" style={{ background: g.color }} />
											{g.label}
											<span className="font-mono text-[11.5px] text-fg3">{formatCount(n)}</span>
										</button>
									);
								})}
							</div>
							<ToggleGroup
								variant="segmented"
								aria-label="Color by"
								value={[colorBy]}
								onValueChange={(v: string[]) => {
									const next = v[0];
									if (next === "level" || next === "resource") setParam("color", next === "level" ? null : next, true);
								}}
							>
								<ToggleGroupItem value="level">Level</ToggleGroupItem>
								<ToggleGroupItem value="resource">Resource</ToggleGroupItem>
							</ToggleGroup>
						</div>
						<VolumeBars
							buckets={buckets}
							label="Log volume of the loaded lines. Arrow keys move between bars, Enter zooms in."
							onPick={(i) => {
								setRange({ kind: "absolute", from: from + i * bw, to: from + (i + 1) * bw });
							}}
						/>
						<div className="flex justify-between font-mono text-[10.5px] text-fg4">
							<span>{formatHourMinute(from)}</span>
							<span>{formatHourMinute(from + (to - from) / 2)}</span>
							<span>{tail || !away ? "now" : formatHourMinute(to)}</span>
						</div>
						<span className="flex items-center gap-1.5 text-[12px] text-fg3">
							Bars count the {formatCount(rows.length)} loaded lines. Volume over the whole range
							<SoonTag />
						</span>
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
										setPage(0);
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
								pageSizes={PAGE_SIZES}
								pageSize={size}
								onPageSize={(n) => {
									setParam("size", n === DEFAULT_PAGE_SIZE ? null : String(n), true);
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
