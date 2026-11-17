import { useListNavigation } from "@/hooks/useListNavigation";
import { useState } from "react";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { Link } from "react-router";
import { HammerIcon, PlusIcon, RocketIcon, RotateCwIcon, ScalingIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { ErrorState } from "@/components/design/ErrorState";
import { Pager } from "@/components/design/Pager";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceEvents } from "@/hooks/useWorkspaceEvents";
import { formatCount } from "@/lib/format";
import { pagedRows, pagingParsers, type Paging } from "@/lib/paging";
import { newResourcePath } from "@/lib/routes";
import { agoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { filteredResources, useObs } from "./context";
import { EventDetail } from "./EventDetail";
import { eventValue, eventItem, eventWindow, severityStyle, type EventItem, type Severity } from "./events";
import { NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import { liveTokens, passes, queryStr, type FieldKey, type Token } from "./query";
import { QueryBar } from "./QueryBar";
import { PAGE_SIZES } from "./shared";
import { LiveButton } from "./Toolbar";
import { ViewHeader } from "./ViewHeader";

const DEFAULT_PAGE_SIZE = 25;
const EVENTS_PARAMS = {
	...pagingParsers(PAGE_SIZES, DEFAULT_PAGE_SIZE),
	live: parseAsStringLiteral(["0"]),
	event: parseAsString,
};
const NOW_TICK_MS = 30_000;

type Show = "all" | "warning" | "error";

function EventIcon({ item }: { item: EventItem }) {
	const icon = "size-3.5 text-fg3";
	if (item.type === "deployment.created") return <RocketIcon aria-hidden className={icon} />;
	if (item.type === "resource.scaled") return <ScalingIcon aria-hidden className={icon} />;
	if (item.type === "resource.restarted") return <RotateCwIcon aria-hidden className={icon} />;
	if (item.type.startsWith("build.") && item.severity === "normal") return <HammerIcon aria-hidden className={icon} />;
	return (
		<StatusGlyph
			glyph={item.type === "resource.recovered" ? "ok" : severityStyle(item.severity).glyph}
			className={cn(item.severity === "normal" && "opacity-60")}
		/>
	);
}

const SHOW_TOKENS: Record<Exclude<Show, "all">, Token> = {
	warning: { neg: false, key: "severity", value: "warning" },
	error: { neg: false, key: "severity", value: "error" },
};

export function EventsView() {
	const { orgId, workspaceId, resources, resourcesLoading, range, tokens, text, setQuery } = useObs();
	const [params, setParams] = useQueryStates(EVENTS_PARAMS, { history: "replace" });
	const live = params.live === null;
	const [openedAt] = useState(Date.now);
	const { active: environment } = useEnvironments();
	const now = useNow(NOW_TICK_MS);
	const nowMs = now.getTime();

	const { events, isLoading, error, refetch } = useWorkspaceEvents(
		workspaceId,
		resources.map((r) => r.raw),
		live,
	);
	const { from, to } = eventWindow(range, nowMs, tsMs(events[0]?.timestamp));
	const visible = new Set(filteredResources("events", resources, tokens).map((r) => r.id));
	const words = text.toLowerCase().split(" ").filter(Boolean);
	const inRange = events
		.filter((e) => {
			const ts = tsMs(e.timestamp);
			return ts >= from && ts <= to;
		})
		.map(eventItem);
	const all = inRange.filter((e) => visible.has(e.resourceId));
	const sevCounts: Record<Severity, number> = { error: 0, warning: 0, normal: 0 };
	for (const e of all) sevCounts[e.severity]++;
	const keys = ["type", "severity", "region", "reason", "actor"] as const;
	const items = all.filter(
		(e) =>
			keys.every((key) => passes([eventValue(e, key)], liveTokens("events", tokens, key))) &&
			words.every((w) => `${e.type} ${e.title} ${e.reason} ${e.detail} ${e.resourceName}`.toLowerCase().includes(w)),
	);
	const show: Show = tokens.some((t) => t.key === "severity" && !t.neg && t.value === "error")
		? "error"
		: tokens.some((t) => t.key === "severity" && !t.neg && t.value === "warning")
			? "warning"
			: "all";

	const paging = { page: params.page, size: params.size };
	const setPaging = (next: Paging) => {
		void setParams(next);
	};
	const paged = pagedRows(items, paging, setPaging, PAGE_SIZES, "0");
	const selKey = params.event;
	const selIdx = selKey !== null ? items.findIndex((x) => x.id === selKey) : -1;
	const selItem = selIdx >= 0 ? items[selIdx] : undefined;
	const select = (key: string | null) => {
		const idx = key === null ? -1 : items.findIndex((x) => x.id === key);
		void setParams(idx >= 0 ? { event: key, page: Math.floor(idx / paging.size) } : { event: key });
	};
	const loading = isLoading || resourcesLoading;
	const query = queryStr(tokens, text);
	const valueCounts = (key: FieldKey): [string, number][] => {
		const counts = new Map<string, number>();
		for (const e of inRange) {
			const value = eventValue(e, key);
			if (value !== "") counts.set(value, (counts.get(value) ?? 0) + 1);
		}
		return [...counts];
	};
	const onListKeyDown = useListNavigation(
		paged.rows.map((e) => e.id),
		selItem?.id ?? null,
		select,
	);

	const header = (
		<ViewHeader
			actions={
				<LiveButton
					on={live}
					detail="streaming"
					title={live ? "Pause event stream" : "Stream new events"}
					onToggle={() => {
						void setParams({ live: live ? "0" : null });
					}}
				/>
			}
		/>
	);

	if (!loading && error !== null) {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ErrorState error={error} what="events" rpc={listEvents} onRetry={refetch} />
			</>
		);
	}
	if (!loading && inRange.length === 0 && query === "") {
		return (
			<>
				{header}
				<QueryBar valueCounts={valueCounts} />
				<ObsHero
					view="events"
					title={environment === undefined ? "No events yet" : `No events yet in ${environment.name}`}
					body="Deploys, restarts, failed health checks and scaling show up here as they happen."
					action={
						<>
							<Button
								nativeButton={false}
								render={<Link to={newResourcePath(orgId, workspaceId, environment ? { env: environment.name } : {})} />}
							>
								<PlusIcon />
								New resource
							</Button>
							<WidenRangeButton />
						</>
					}
				/>
			</>
		);
	}

	return (
		<>
			{header}
			<QueryBar valueCounts={valueCounts} />
			<div>
				<section aria-label="Events" className="flex min-w-0 flex-col rounded-card bg-panel">
					<div className="flex flex-wrap items-center gap-2.5 px-5 pt-3.5 pb-2.5">
						<ToggleGroup
							variant="segmented"
							aria-label="Show"
							value={[show]}
							onValueChange={(v: string[]) => {
								const rest = tokens.filter((t) => t.key !== "severity");
								const next = v[0];
								setQuery(next === "warning" || next === "error" ? [...rest, SHOW_TOKENS[next]] : rest, text);
							}}
						>
							<ToggleGroupItem value="all" count={formatCount(all.length)}>
								All
							</ToggleGroupItem>
							<ToggleGroupItem value="warning" count={formatCount(sevCounts.warning)}>
								Warnings
							</ToggleGroupItem>
							<ToggleGroupItem value="error" count={formatCount(sevCounts.error)}>
								Errors
							</ToggleGroupItem>
						</ToggleGroup>
					</div>
					<div className="overflow-x-auto px-5 [scrollbar-width:thin]">
						<table className="w-full min-w-[640px] border-collapse text-[13px]">
							<thead>
								<tr className="h-[38px] border-b border-line2 text-left text-[12.5px] text-fg3">
									<th className="w-[18px] font-normal">
										<span className="sr-only">Severity</span>
									</th>
									<th className="px-3 font-normal">Event</th>
									<th className="w-[160px] px-3 font-normal">Resource</th>
									<th className="w-[110px] pl-3 text-right font-normal">Time</th>
								</tr>
							</thead>
							<tbody>
								{loading &&
									Array.from({ length: 8 }, (_, i) => (
										<tr key={String(i)} className="h-[58px] border-b border-line2">
											<td colSpan={4}>
												<Skeleton className="h-3 w-full" />
											</td>
										</tr>
									))}
								{!loading &&
									paged.rows.map((e) => {
										const on = e.id === selItem?.id;
										return (
											<tr
												key={e.id}
												role="button"
												tabIndex={0}
												aria-label={e.title}
												aria-pressed={on}
												onClick={() => {
													select(e.id);
												}}
												onKeyDown={(ev) => {
													if (ev.key === "Enter" || ev.key === " ") {
														ev.preventDefault();
														select(e.id);
													}
												}}
												className={cn(
													"h-[58px] border-b border-line2 last:border-b-0 hover:bg-panel2",
													on && "bg-background",
													e.ts > openedAt && "animate-row-flash motion-reduce:animate-none",
												)}
											>
												<td>
													<EventIcon item={e} />
													<span className="sr-only">{e.severity}</span>
												</td>
												<td className="max-w-0 px-3">
													<div className="flex min-w-0 flex-col gap-1">
														<span className={cn("font-medium", e.severity === "error" && "text-bad-fg")}>
															{e.title}
														</span>
														<span className="w-full truncate font-mono text-[12px] text-fg3" title={e.detail}>
															{e.detail}
														</span>
													</div>
												</td>
												<td className="px-3 text-fg2">{e.resourceName}</td>
												<td className="pl-3 text-right font-mono text-[12px] whitespace-nowrap text-fg3">
													{agoLabel(e.ts, nowMs)}
												</td>
											</tr>
										);
									})}
							</tbody>
						</table>
						{!loading && items.length === 0 && <NoMatches title="No events match these filters" />}
					</div>
					<Pager {...paged.pager} {...paged.ends} />
				</section>
				<EventDetail
					onKeyDown={onListKeyDown}
					item={selItem ?? null}
					onPrev={
						selIdx > 0
							? () => {
									select(items[selIdx - 1]?.id ?? null);
								}
							: null
					}
					onNext={
						selIdx < items.length - 1
							? () => {
									select(items[selIdx + 1]?.id ?? null);
								}
							: null
					}
					onClose={() => {
						select(null);
					}}
				/>
			</div>
		</>
	);
}
