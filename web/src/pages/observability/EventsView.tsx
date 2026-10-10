import { useState } from "react";
import { ActivityIcon, SearchXIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceEvents } from "@/hooks/useWorkspaceEvents";
import { getErrorMessage } from "@/lib/error-handler";
import { timeRangeMs } from "@/lib/obs";
import { pageRangeLabel, pageSlice } from "@/lib/paging";
import { formatClock, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { EVENTS_MAX_RANGE, selectedResources, useObs } from "./context";
import { EventDetail } from "./EventDetail";
import { groupEvents, severityOf, severityStyle, type Severity } from "./events";
import { fmtTs } from "./format";
import { ObsGate } from "./ObsGate";
import { WidenRangeButton } from "./ObsEmpty";
import { Dot, Histogram, PAGE_SIZES, type HistoBucket } from "./shared";
import { ResourceMenu, TimeRangeMenu } from "./Toolbar";

type TypeFilter = "all" | Severity;

const TYPES: { key: TypeFilter; label: string }[] = [
	{ key: "all", label: "All" },
	{ key: "error", label: "Errors" },
	{ key: "warning", label: "Warnings" },
	{ key: "normal", label: "Normal" },
];

const BUCKETS = 48;

function isTypeFilter(v: string | undefined): v is TypeFilter {
	return v === "all" || v === "error" || v === "warning" || v === "normal";
}

export function EventsView() {
	const { workspaceId, resources, resourcesLoading, selected, range, resourceByName } = useObs();
	const [query, setQuery] = useState("");
	const [type, setType] = useState<TypeFilter>("all");
	const [page, setPage] = useState(0);
	const [size, setSize] = useState(25);
	const [selKey, setSelKey] = useState<string | null>(null);
	const now = useNow(30_000);
	const nowMs = now.getTime();
	const from = nowMs - timeRangeMs(range);

	const sel = selectedResources(resources, selected);
	const { events, isLoading, error, unavailable } = useWorkspaceEvents(workspaceId, resources.map((r) => r.raw));
	const selIds = new Set(sel.map((r) => r.id));
	const q = query.trim().toLowerCase();
	const base = events.filter((e) => {
		const ts = tsMs(e.timestamp);
		if (ts < from || !selIds.has(e.resourceId)) return false;
		if (q === "") return true;
		return `${e.reason} ${e.message} ${e.podName} ${e.resourceName}`.toLowerCase().includes(q);
	});
	const sevCounts: Record<Severity, number> = { error: 0, warning: 0, normal: 0 };
	const filtered: typeof base = [];
	for (const e of base) {
		const s = severityOf(e.type, e.reason);
		sevCounts[s]++;
		if (type === "all" || s === type) filtered.push(e);
	}
	const items = groupEvents(filtered);

	const { rows: pageItems, page: curPage, pages } = pageSlice(items, page, size);
	const selIdx = selKey !== null ? items.findIndex((x) => x.key === selKey) : -1;
	const selItem = selIdx >= 0 ? items[selIdx] : undefined;

	const bw = timeRangeMs(range) / BUCKETS;
	const counts = Array.from({ length: BUCKETS }, () => ({ e: 0, w: 0, n: 0 }));
	for (const e of filtered) {
		const i = Math.max(0, Math.min(BUCKETS - 1, Math.floor((tsMs(e.timestamp) - from) / bw)));
		const b = counts[i];
		if (b === undefined) continue;
		const s = severityOf(e.type, e.reason);
		if (s === "error") b.e++;
		else if (s === "warning") b.w++;
		else b.n++;
	}
	const histo: HistoBucket[] = counts.map((b, i) => ({
		title: `${fmtTs(from + i * bw)} · ${String(b.e)} errors, ${String(b.w)} warnings, ${String(b.n)} normal`,
		segments: [
			{ value: b.e, className: "bg-err" },
			{ value: b.w, className: "bg-warn" },
			{ value: b.n, className: "bg-line2" },
		],
	}));

	const open = selItem !== undefined;
	const rowCols = open ? "84px 150px minmax(0,1fr) minmax(0,1fr) 48px" : "120px 170px minmax(0,1.1fr) 104px minmax(0,1.6fr) 56px";
	const select = (key: string | null) => {
		setSelKey(key);
		if (key === null) return;
		const idx = items.findIndex((x) => x.key === key);
		if (idx >= 0) setPage(Math.floor(idx / size));
	};
	const loading = isLoading || resourcesLoading;
	const toolbar = (
		<div className="flex flex-wrap items-center gap-2">
			<ResourceMenu />
			<div className="flex-1" />
			<TimeRangeMenu maxRange={null} />
		</div>
	);

	const filtering = q !== "" || type !== "all";
	const widen = range === EVENTS_MAX_RANGE ? undefined : <WidenRangeButton max={EVENTS_MAX_RANGE} />;
	const clearFilters = () => {
		setQuery("");
		setType("all");
		setPage(0);
	};

	if (!loading && unavailable) {
		return (
			<ObsGate kind="events">
				{toolbar}
				<section className="rounded-lg border border-line bg-background">
					<EmptyState
						title={
							<span className="flex items-center gap-2">
								Events aren&apos;t available yet
								<SoonTag />
							</span>
						}
					>
						Events for your services will show up here once Loco records them.
					</EmptyState>
				</section>
			</ObsGate>
		);
	}

	return (
		<ObsGate kind="events">
			{toolbar}
			<div className="grid items-start gap-5" style={{ gridTemplateColumns: open ? "minmax(0,1fr) minmax(340px,400px)" : "minmax(0,1fr)" }}>
				<div className="flex min-w-0 flex-col gap-3">
					<div className="flex flex-wrap items-center gap-2">
						<SearchInput
							value={query}
							onChange={(e) => {
								setQuery(e.target.value);
								setPage(0);
							}}
							placeholder="Search reason, object or message"
							className="min-w-[220px] flex-1"
						/>
						<ToggleGroup
							variant="segmented"
							value={[type]}
							onValueChange={(v: string[]) => {
								const next = v[0];
								if (isTypeFilter(next)) {
									setType(next);
									setPage(0);
								}
							}}
						>
							{TYPES.map((t) => (
								<ToggleGroupItem key={t.key} value={t.key} className="h-[26px]! gap-1.5 text-sm">
									<span className={cn("size-[7px] rounded-[2px]", t.key === "all" ? "bg-transparent" : severityStyle(t.key).dot)} />
									{t.label}
									<span className="text-xs font-normal text-fg3 tabular-nums">
										{t.key === "all" ? base.length : sevCounts[t.key]}
									</span>
								</ToggleGroupItem>
							))}
						</ToggleGroup>
					</div>
					<section className="rounded-lg border border-line bg-background">
						<Histogram buckets={histo} from={fmtTs(from)} height={64} />
					</section>
					<section className="overflow-hidden rounded-lg border border-line bg-background">
						<div className="overflow-x-auto">
							<div className="min-w-[720px]">
								<div
									className="grid h-[34px] items-center gap-3 border-b border-line bg-bg2 px-4 text-sm font-semibold text-fg2"
									style={{ gridTemplateColumns: rowCols }}
								>
									<span>Last seen</span>
									<span>Reason</span>
									<span>Object</span>
									{!open && (
										<span className="flex items-center gap-1.5 text-fg4">
											Deployment
											<SoonTag />
										</span>
									)}
									<span>Message</span>
									<span className="text-right">Count</span>
								</div>
								{loading &&
									Array.from({ length: 8 }, (_, i) => (
										<div key={String(i)} className="flex h-10 items-center gap-3 border-b border-line px-4">
											<Skeleton className="h-3 w-[100px]" />
											<Skeleton className="h-3 w-[120px]" />
											<Skeleton className="h-3 w-1/4" />
											<Skeleton className="h-3 w-1/3" />
										</div>
									))}
								{!loading &&
									pageItems.map((e) => {
										const st = severityStyle(e.severity);
										const on = e.key === selItem?.key;
										const res = resourceByName.get(e.resourceName);
										return (
											<div
												key={e.key}
												onClick={() => {
													select(on ? null : e.key);
												}}
												className={cn(
													"grid min-h-10 cursor-pointer items-center gap-3 border-b border-l-[3px] border-b-line py-1 pr-4 pl-[13px] [contain-intrinsic-block-size:auto_42px] [content-visibility:auto] hover:bg-bg2",
													on ? "border-l-primary bg-info-bg shadow-[inset_0_0_0_1px_var(--accent)]" : st.bar,
												)}
												style={{ gridTemplateColumns: rowCols }}
											>
												<span className="whitespace-nowrap text-fg3 tabular-nums">{open ? formatClock(e.ts) : fmtTs(e.ts)}</span>
												<span className="flex min-w-0 items-center gap-[7px]">
													<span className={cn("size-[7px] shrink-0 rounded-[2px]", st.dot)} />
													<span className={cn("truncate font-semibold", st.fg)}>{e.reason}</span>
												</span>
												<span className="flex min-w-0 flex-col">
													<span className="flex items-center gap-1.5 font-medium">
														<Dot color={res?.color ?? "var(--fg4)"} />
														{e.resourceName}
													</span>
													<span className="truncate font-mono text-xs text-fg3">{e.object}</span>
												</span>
												{!open && <span className="font-mono text-sm text-fg4">—</span>}
												<span className="truncate text-fg2" title={e.message}>
													{e.message}
												</span>
												<span className="flex justify-end">
													{e.occ.length > 1 && (
														<span className={cn("inline-flex h-5 items-center rounded-full px-[7px] text-[11.5px] font-semibold tabular-nums", st.count)}>
															×{e.occ.length}
														</span>
													)}
												</span>
											</div>
										);
									})}
								{!loading && error !== null && (
									<div className="px-4 py-6 text-bad-fg">{getErrorMessage(error, "Failed to load events")}</div>
								)}
								{!loading && error === null && items.length === 0 && (
									<div className="border-b border-line">
										{filtering ? (
											<EmptyState
												icon={<SearchXIcon />}
												title="No events match"
												query={query.trim()}
												action={
													<>
														<Button variant="outline" onClick={clearFilters}>
															<XIcon />
															Clear filters
														</Button>
														{widen}
													</>
												}
											/>
										) : (
											<EmptyState icon={<ActivityIcon />} title={`No events in the last ${range}`} action={widen} />
										)}
									</div>
								)}
							</div>
						</div>
						<Pager
							className="bg-bg2"
							label={items.length > 0 ? pageRangeLabel(curPage, size, items.length) : "0"}
							pageSizes={PAGE_SIZES}
							pageSize={size}
							onPageSize={(n) => {
								setSize(n);
								setPage(0);
							}}
							onPrev={curPage > 0 ? () => { setPage(curPage - 1); } : null}
							onNext={curPage < pages - 1 ? () => { setPage(curPage + 1); } : null}
						/>
					</section>
				</div>
				{selItem !== undefined && (
					<EventDetail
						item={selItem}
						nowMs={nowMs}
						onPrev={selIdx > 0 ? () => { select(items[selIdx - 1]?.key ?? null); } : null}
						onNext={selIdx < items.length - 1 ? () => { select(items[selIdx + 1]?.key ?? null); } : null}
						onClose={() => {
							select(null);
						}}
					/>
				)}
			</div>
		</ObsGate>
	);
}
