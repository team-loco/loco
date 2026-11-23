import { useListNavigation } from "@/hooks/useListNavigation";
import { useEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQueryClient } from "@tanstack/react-query";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { EventService, type ListEventsRequest, type ListEventsRequestSchema } from "@gen/loco/event/v1/event_pb";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { Link } from "react-router";
import { PlusIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { ErrorState } from "@/components/design/ErrorState";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Pager } from "@/components/design/Pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { eventTypesQuery } from "@/lib/audit-query";
import { useHeldError } from "@/hooks/useHeldError";
import { useTokenPage } from "@/hooks/useTokenPage";
import { formatTotal, pageTokensParser, pagingParsers, tokenPageLabel, tokenPager } from "@/lib/paging";
import { resolveWindow } from "@/lib/obs-time";
import { newResourcePath } from "@/lib/routes";
import { agoLabel, msToTimestamp } from "@/lib/time";
import { ENVIRONMENT_EVENT_TYPES, eventInEnvironment, runtimeEvent } from "@/lib/runtime-events";
import { cn } from "@/lib/utils";

import { useObs } from "./context";
import { EventDetail, EventIcon } from "./EventDetail";
import { eventItem, type EventItem, type Severity } from "./events";
import { coalesced } from "./coalesce";
import { eventsQuery } from "./eventsQuery";
import { ObsHero, WidenRangeButton } from "./ObsEmpty";
import { liveTokens, type FieldKey, type Token } from "./query";
import { QueryBar } from "./QueryBar";
import { PAGE_SIZES } from "./shared";
import { LiveButton } from "./Toolbar";
import { ViewHeader } from "./ViewHeader";

const DEFAULT_PAGE_SIZE = 25;
// Page tokens sit in the obs view's page parameter, which query and range changes clear.
const EVENTS_PARAMS = {
	page: pageTokensParser,
	size: pagingParsers(PAGE_SIZES, DEFAULT_PAGE_SIZE).size,
	live: parseAsStringLiteral(["0"]),
	event: parseAsString,
};
const NOW_TICK_MS = 30_000;
// Live mode without the event stream polls the page this often, and the counts on a slower clock.
const POLL_MS = 10_000;
const FACETS_MS = 60_000;
// With the stream, a burst of new events refreshes the page and the counts at most this often.
const PAGE_REFRESH_MS = 2_000;
const COUNT_REFRESH_MS = 10_000;
const EVENT_KEYS = ["type", "severity", "region", "reason", "actor", "resource"] as const;

type Show = "all" | "warning" | "error";

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

	const span = resolveWindow(range, nowMs);
	const base = {
		orgId,
		workspaceId,
		environmentId: environment?.id ?? "",
		types: ENVIRONMENT_EVENT_TYPES,
		readableResources: true,
		startTime: msToTimestamp(span.from),
		...(range.kind === "absolute" ? { endTime: msToTimestamp(span.to) } : {}),
	};
	const enabled = orgId !== "" && workspaceId !== "" && environment !== undefined;
	const chips = EVENT_KEYS.flatMap((key) => liveTokens("events", tokens, key));
	const resourceChips = chips.filter((t) => t.key === "resource");
	const query = eventsQuery(chips, text);
	const canStream = useMyScopes().resourceGrants(orgId, workspaceId) === "workspace";
	const stream = useEventStream({
		orgId,
		workspaceId,
		openedAt,
		on: live && canStream && enabled,
		environmentId: environment?.id ?? "",
		query,
		onFirstPage: params.page.length === 0,
	});
	const streamFailed = stream.failed;
	const poll = live && (!canStream || streamFailed) ? POLL_MS : false;
	const page = useTokenPage(listEvents, { ...base, query, pageSize: params.size }, params.page, {
		enabled,
		refetchInterval: poll,
		placeholderData: keepPreviousData,
	});
	const facets = useQuery(
		listEvents,
		{ ...base, pageSize: 1, includeFacets: true },
		{
			enabled,
			refetchInterval: poll === false ? false : FACETS_MS,
			placeholderData: keepPreviousData,
		},
	);
	const counted = { base, resourceChips, enabled, live: poll !== false };
	const allCount = useSeverityCount(counted, null);
	const warningCount = useSeverityCount(counted, "warning");
	const errorCount = useSeverityCount(counted, "error");
	const names = new Map(resources.map((r) => [r.id, r.slug]));
	const items = (page.query.data?.events ?? [])
		.map((e) => ({
			...runtimeEvent(e),
			resourceId: e.resourceId,
			resourceName: names.get(e.resourceId) ?? "",
		}))
		.filter((e) => eventInEnvironment(e, environment?.id ?? ""))
		.map(eventItem);
	const error = useHeldError(page.query.error ?? facets.error, page.query.isFetching || facets.isFetching);
	const refetch = async () => await Promise.all([page.query.refetch(), facets.refetch()]);
	const show: Show = tokens.some((t) => t.key === "severity" && !t.neg && t.value === "error")
		? "error"
		: tokens.some((t) => t.key === "severity" && !t.neg && t.value === "warning")
			? "warning"
			: "all";

	const pager = tokenPager(params.page, page.nextPageToken, (pages) => {
		if (!page.query.isPlaceholderData) void setParams({ page: pages });
	});
	const selKey = params.event;
	const selIdx = selKey !== null ? items.findIndex((x) => x.id === selKey) : -1;
	const selItem = selIdx >= 0 ? items[selIdx] : undefined;
	const select = (key: string | null) => {
		void setParams({ event: key });
	};
	const loading = resourcesLoading || (error === null && page.query.isPending);
	const valueCounts = (key: FieldKey): [string, number][] =>
		(facets.data?.facets.find((f) => f.key === key)?.values ?? []).map((v) => [v.value, v.count]);
	const onListKeyDown = useListNavigation(
		items.map((e) => e.id),
		selItem?.id ?? null,
		select,
	);
	const table = useDataTable("events", {
		columns: eventColumns(nowMs),
		data: items,
		getRowId: (e) => e.id,
	});

	const header = (
		<ViewHeader
			actions={
				<LiveButton
					on={live}
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
	if (!loading && allCount === 0 && query === "") {
		const hasResources = resources.length > 0;
		return (
			<>
				{header}
				<ObsHero
					view="events"
					title={hasResources ? "No events in this range" : "No events yet"}
					body={`Deploys, restarts, failed health checks and scaling${environment === undefined ? "" : ` in ${environment.name}`} show up here as they happen.`}
					action={
						hasResources ? (
							<WidenRangeButton />
						) : (
							<Button
								nativeButton={false}
								render={<Link to={newResourcePath(orgId, workspaceId, environment ? { env: environment.slug } : {})} />}
							>
								<PlusIcon />
								New resource
							</Button>
						)
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
							<ToggleGroupItem value="all" count={countLabel(allCount)}>
								All
							</ToggleGroupItem>
							<ToggleGroupItem value="warning" count={countLabel(warningCount)}>
								Warnings
							</ToggleGroupItem>
							<ToggleGroupItem value="error" count={countLabel(errorCount)}>
								Errors
							</ToggleGroupItem>
						</ToggleGroup>
						{stream.newEvents > 0 && pager.onFirst !== null && (
							<Button variant="ghost" size="sm" onClick={pager.onFirst}>
								{stream.newEvents === 1 ? "1 new event" : `${formatTotal(stream.newEvents)} new events`}
							</Button>
						)}
					</div>
					<DataTable
						table={table}
						label="Events"
						loading={loading}
						empty={<div className="px-6 py-7 text-center text-[13px] text-fg3">No events match.</div>}
						activeRowId={selItem?.id ?? null}
						onRowClick={(row) => {
							select(row.id);
						}}
						rowLabel={(row) => row.original.title}
						rowClassName={(row) => row.original.ts > openedAt && "animate-row-flash motion-reduce:animate-none"}
					/>
					<Pager
						label={tokenPageLabel(pager.page, params.size, items.length, page.total)}
						page={pager.page}
						total={items.length}
						pageSizes={PAGE_SIZES}
						pageSize={params.size}
						onPageSize={(size) => {
							void setParams({ size: PAGE_SIZES.find((n) => n === size) ?? null, page: null });
						}}
						onFirst={pager.onFirst}
						onPrev={pager.onPrev}
						onNext={pager.onNext}
					/>
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

/**
 * Streams the environment's new events that query matches while on. On the first page a burst of events
 * refetches the page at most once per PAGE_REFRESH_MS and the counts at most once per COUNT_REFRESH_MS,
 * one request at a time; on later pages it counts the new events instead of moving rows under the reader.
 */
function useEventStream({
	orgId,
	workspaceId,
	environmentId,
	query,
	openedAt,
	on,
	onFirstPage,
}: {
	orgId: string;
	workspaceId: string;
	environmentId: string;
	query: string;
	openedAt: number;
	on: boolean;
	onFirstPage: boolean;
}): { failed: boolean; newEvents: number } {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const key = `${orgId}/${workspaceId}`;
	const [failed, setFailed] = useState<string | null>(null);
	const [unseen, setUnseen] = useState({ key, count: 0 });
	if (onFirstPage && unseen.count > 0) setUnseen({ key, count: 0 });
	const firstPageRef = useRef(onFirstPage);
	useEffect(() => {
		firstPageRef.current = onFirstPage;
	}, [onFirstPage]);
	useEffect(() => {
		if (!on) return;
		const abort = new AbortController();
		// Refetches this view's listed pages, or its one-row count and facet requests.
		const invalidate = (counts: boolean) => async () => {
			await queryClient.invalidateQueries({
				queryKey: createConnectQueryKey({ schema: listEvents, cardinality: "finite" }),
				predicate: (q) => {
					const input = (q.queryKey[1] as { input?: Partial<ListEventsRequest> } | undefined)?.input;
					return input?.readableResources === true && (input.pageSize === 1) === counts;
				},
			});
		};
		const pageRefresh = coalesced(invalidate(false), PAGE_REFRESH_MS);
		const countRefresh = coalesced(invalidate(true), COUNT_REFRESH_MS);
		const consume = async () => {
			try {
				const client = createClient(EventService, transport);
				for await (const message of client.streamEvents(
					{
						orgId,
						workspaceId,
						environmentId,
						startTime: msToTimestamp(openedAt),
						query: [eventTypesQuery(ENVIRONMENT_EVENT_TYPES), query === "" ? "" : `(${query})`].join(" ").trim(),
					},
					{ signal: abort.signal },
				)) {
					if (message.events.length === 0) continue;
					countRefresh.poke();
					if (firstPageRef.current) pageRefresh.poke();
					else
						setUnseen((u) => ({
							key,
							count: (u.key === key ? u.count : 0) + message.events.length,
						}));
				}
			} catch {
				if (!abort.signal.aborted) setFailed(key);
			}
		};
		void consume();
		return () => {
			abort.abort();
			pageRefresh.stop();
			countRefresh.stop();
		};
	}, [on, orgId, workspaceId, environmentId, query, openedAt, transport, queryClient, key]);
	return { failed: failed === key, newEvents: unseen.key === key ? unseen.count : 0 };
}

/** The events the resource chips keep, of one severity or all, for the severity tabs. */
function useSeverityCount(
	{
		base,
		resourceChips,
		enabled,
		live,
	}: {
		base: Omit<MessageInitShape<typeof ListEventsRequestSchema>, "$typeName">;
		resourceChips: Token[];
		enabled: boolean;
		live: boolean;
	},
	severity: Exclude<Severity, "normal"> | null,
): number | undefined {
	const chips = severity === null ? resourceChips : [...resourceChips, SHOW_TOKENS[severity]];
	return useQuery(
		listEvents,
		{ ...base, query: eventsQuery(chips, ""), pageSize: 1, includeTotalCount: true },
		{ enabled, refetchInterval: live ? FACETS_MS : false },
	).data?.totalCount;
}

function countLabel(count: number | undefined): string | undefined {
	return count === undefined ? undefined : formatTotal(count);
}

function eventColumns(nowMs: number): DataColumn<EventItem>[] {
	return [
		{
			id: "severity",
			size: 40,
			minSize: 40,
			enableResizing: false,
			meta: { label: "Severity", fit: true },
			cell: ({ row: { original: e } }) => (
				<>
					<EventIcon item={e} />
					<span className="sr-only">{e.severity}</span>
				</>
			),
		},
		{
			id: "event",
			header: "Event",
			size: 320,
			meta: { grow: true, primary: true },
			cell: ({ row: { original: e } }) => (
				<span className="flex min-w-0 flex-col gap-0.5">
					<CellText text={e.title} className={cn("text-[13.5px]", e.severity === "error" && "text-bad-fg")} />
					<CellText text={e.detail} className="font-mono text-[11.5px] text-fg3" />
				</span>
			),
		},
		{
			id: "resource",
			header: "Resource",
			size: 160,
			meta: { fit: true, fitMax: 280 },
			cell: ({ row }) => <CellText text={row.original.resourceName} className="text-fg2" />,
		},
		{
			id: "time",
			header: "Time",
			size: 110,
			meta: { align: "end" },
			cell: ({ row }) => (
				<span className="truncate font-mono text-[12px] whitespace-nowrap text-fg3">
					{agoLabel(row.original.ts, nowMs)}
				</span>
			),
		},
	];
}
