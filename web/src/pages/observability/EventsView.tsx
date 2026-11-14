import { useState } from "react";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
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
import { resolveWindow } from "@/lib/obs-time";
import { pagedRows } from "@/lib/paging";
import { newResourcePath, resourcePath } from "@/lib/routes";
import { agoLabel, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { filteredResources, useObs } from "./context";
import { EventDetail } from "./EventDetail";
import { eventSummary, eventTitle, groupEvents, severityStyle, type EventGroup, type Severity } from "./events";
import { NoMatches, ObsHero, WidenRangeButton } from "./ObsEmpty";
import { liveTokens, passes, queryStr, type FieldKey, type Token } from "./query";
import { QueryBar } from "./QueryBar";
import { PAGE_SIZES, readPaging } from "./shared";
import { LiveButton } from "./Toolbar";
import { ViewHeader } from "./ViewHeader";

const DEFAULT_PAGE_SIZE = 25;
const POLL_MS = 10_000;
const NOW_TICK_MS = 30_000;

type Show = "all" | "warning" | "error";

function EventIcon({ group }: { group: EventGroup }) {
	const icon = "size-3.5 text-fg3";
	if (group.type === "deployment.created") return <RocketIcon aria-hidden className={icon} />;
	if (group.type === "resource.scaled") return <ScalingIcon aria-hidden className={icon} />;
	if (group.type === "resource.restarted") return <RotateCwIcon aria-hidden className={icon} />;
	if (group.type.startsWith("build.") && group.severity === "normal")
		return <HammerIcon aria-hidden className={icon} />;
	return (
		<StatusGlyph
			glyph={group.type === "resource.recovered" ? "ok" : severityStyle(group.severity).glyph}
			className={cn(group.severity === "normal" && "opacity-60")}
		/>
	);
}

const SHOW_TOKENS: Record<Exclude<Show, "all">, Token> = {
	warning: { neg: false, key: "type", value: "warning" },
	error: { neg: false, key: "type", value: "error" },
};

function typeValues(severity: Severity): string[] {
	switch (severity) {
		case "error":
			return ["error", "warning"];
		case "warning":
			return ["warning"];
		case "normal":
			return ["normal"];
	}
}

export function EventsView() {
	const {
		orgId,
		workspaceId,
		resources,
		resourcesLoading,
		range,
		tokens,
		text,
		setQuery,
		params,
		setParam,
		setPaging,
	} = useObs();
	// Events poll by default; ?live=0 stops it.
	const live = params.get("live") !== "0";
	const [selKey, setSelKey] = useState<string | null>(null);
	const [openedAt] = useState(Date.now);
	const { active: environment } = useEnvironments();
	const now = useNow(live ? POLL_MS : NOW_TICK_MS);
	const nowMs = now.getTime();
	const { from, to } = resolveWindow(range, nowMs);

	const { events, isLoading, error, refetch } = useWorkspaceEvents(
		workspaceId,
		resources.map((r) => r.raw),
		live ? POLL_MS : undefined,
	);
	const visible = new Set(filteredResources("events", resources, tokens).map((r) => r.id));
	const words = text.toLowerCase().split(" ").filter(Boolean);
	const inRange = events.filter((e) => {
		const ts = tsMs(e.timestamp);
		if (ts < from || ts > to || !visible.has(e.resourceId)) return false;
		const hay = `${eventTitle(e.type, e.reason)} ${e.reason} ${eventSummary(e)} ${e.resourceName}`.toLowerCase();
		return words.every((w) => hay.includes(w));
	});
	const typeTokens = liveTokens("events", tokens, "type");
	const all = groupEvents(inRange);
	const sevCounts: Record<Severity, number> = { error: 0, warning: 0, normal: 0 };
	for (const g of all) sevCounts[g.severity]++;
	const items = all.filter((g) => passes(typeValues(g.severity), typeTokens));
	const show: Show = tokens.some((t) => t.key === "type" && !t.neg && t.value === "error")
		? "error"
		: tokens.some((t) => t.key === "type" && !t.neg && t.value === "warning")
			? "warning"
			: "all";

	const paging = readPaging(params, DEFAULT_PAGE_SIZE);
	const paged = pagedRows(
		items,
		paging,
		(next) => {
			setPaging(next, DEFAULT_PAGE_SIZE);
		},
		PAGE_SIZES,
		"0",
	);
	const selIdx = selKey !== null ? items.findIndex((x) => x.key === selKey) : -1;
	const selItem = selIdx >= 0 ? items[selIdx] : undefined;
	const select = (key: string | null) => {
		setSelKey(key);
		if (key === null) return;
		const idx = items.findIndex((x) => x.key === key);
		if (idx >= 0) setPaging({ page: Math.floor(idx / paging.size), size: paging.size }, DEFAULT_PAGE_SIZE);
	};
	const loading = isLoading || resourcesLoading;
	const query = queryStr(tokens, text);
	const valueCounts = (key: FieldKey): [string, number][] => {
		if (key === "resource") return resources.map((r) => [r.name, 0]);
		if (key === "type")
			return [
				["warning", sevCounts.warning + sevCounts.error],
				["error", sevCounts.error],
				["normal", sevCounts.normal],
			];
		return [];
	};

	const header = (
		<ViewHeader
			count={loading || error !== null ? undefined : formatCount(items.length)}
			actions={
				<LiveButton
					on={live}
					detail="every 10s"
					title={live ? "Stop checking for new events" : "Check for new events every 10 seconds"}
					onToggle={() => {
						setParam("live", live ? "0" : null, true);
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
			<div
				className="grid grid-cols-1 items-start gap-4 xl:grid-cols-(--ev-cols)"
				style={
					{
						"--ev-cols": selItem !== undefined ? "minmax(0,1fr) minmax(340px,400px)" : "minmax(0,1fr)",
					} as React.CSSProperties
				}
			>
				<section aria-label="Events" className="flex min-w-0 flex-col rounded-card bg-panel">
					<div className="flex flex-wrap items-center gap-2.5 px-5 pt-3.5 pb-2.5">
						<ToggleGroup
							variant="segmented"
							aria-label="Show"
							value={[show]}
							onValueChange={(v: string[]) => {
								const rest = tokens.filter((t) => t.key !== "type");
								const next = v[0];
								setQuery(next === "warning" || next === "error" ? [...rest, SHOW_TOKENS[next]] : rest, text);
							}}
						>
							<ToggleGroupItem value="all" count={formatCount(all.length)}>
								All
							</ToggleGroupItem>
							<ToggleGroupItem value="warning" count={formatCount(sevCounts.warning + sevCounts.error)}>
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
									<th className="w-[72px] px-3 text-right font-normal">Count</th>
									<th className="w-[110px] pl-3 text-right font-normal">Last seen</th>
								</tr>
							</thead>
							<tbody>
								{loading &&
									Array.from({ length: 8 }, (_, i) => (
										<tr key={String(i)} className="h-[58px] border-b border-line2">
											<td colSpan={5}>
												<Skeleton className="h-3 w-full" />
											</td>
										</tr>
									))}
								{!loading &&
									paged.rows.map((e) => {
										const on = e.key === selItem?.key;
										return (
											<tr
												key={e.key}
												className={cn(
													"h-[58px] border-b border-line2 last:border-b-0",
													on && "bg-background",
													e.ts > openedAt && "animate-row-flash motion-reduce:animate-none",
												)}
											>
												<td>
													<EventIcon group={e} />
													<span className="sr-only">{e.severity}</span>
												</td>
												<td className="max-w-0 px-3">
													<button
														type="button"
														aria-pressed={on}
														onClick={() => {
															select(on ? null : e.key);
														}}
														className="flex w-full min-w-0 flex-col items-start rounded-sm text-left focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
													>
														<span
															className={cn("font-medium", e.severity === "error" && "text-bad-fg")}
															title={e.reason === "" ? e.type : e.reason}
														>
															{e.title}
														</span>
														<span className="w-full truncate font-mono text-[12px] text-fg3" title={e.message}>
															{e.object !== e.resourceName ? `${e.object} · ` : ""}
															{e.message}
														</span>
													</button>
												</td>
												<td className="px-3">
													<Link
														to={resourcePath(orgId, workspaceId, e.resourceId)}
														className="text-fg2 hover:text-foreground hover:underline"
													>
														{e.resourceName}
													</Link>
												</td>
												<td className="px-3 text-right">
													{e.occ.length > 1 && (
														<span className="rounded-sm border border-line2 px-1.5 font-mono text-[11.5px] text-fg2">
															×{e.occ.length}
														</span>
													)}
												</td>
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
				{selItem !== undefined && (
					<EventDetail
						item={selItem}
						onPrev={
							selIdx > 0
								? () => {
										select(items[selIdx - 1]?.key ?? null);
									}
								: null
						}
						onNext={
							selIdx < items.length - 1
								? () => {
										select(items[selIdx + 1]?.key ?? null);
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
	);
}
