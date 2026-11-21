import { useTransport } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { BoxIcon, GitBranchIcon, LayersIcon, PlayIcon, PauseIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import {
	parseAsArrayOf,
	parseAsInteger,
	parseAsNumberLiteral,
	parseAsString,
	parseAsStringLiteral,
	useQueryStates,
} from "nuqs";
import { useLocation, useSearchParams, useParams } from "react-router";

import { Button } from "@/components/design/Button";
import { ErrorState } from "@/components/design/ErrorState";
import { PickFilter } from "@/components/design/FilterPopover";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { orgMembersOptions } from "@/hooks/completeMembers";
import { useAuditAccess } from "@/hooks/useAuditAccess";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { useNow } from "@/hooks/useNow";
import { useTokenPage } from "@/hooks/useTokenPage";
import { msParser, type TimeWindow } from "@/lib/obs-time";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { agoLabel, msToTimestamp } from "@/lib/time";
import { TimeRangePicker } from "./observability/Toolbar";
import { AuditQueryError } from "./audit/AuditQueryError";
import { auditSearchError, insertAuditField } from "./audit/queryError";

import { AUDIT_FILTER_FIXES, auditLink, auditFilterViolations, type DroppedFilter } from "./audit/link";
import { DroppedFilters, AuditFilterError } from "./audit/AuditFeedback";
import { AuditDrawer } from "./audit/AuditDrawer";
import { ActorFilter, EventFilter } from "./audit/AuditFilters";
import { AuditTable } from "./audit/AuditTable";
import { ACTOR_KINDS, auditQuery, auditRow, DEFAULT_RANGE, PAGE_SIZES, RANGES, TYPE_ACTORS } from "./audit/model";
import { useWorkspaceDetails } from "./audit/useWorkspaceDetails";
import { AUDIT_EVENT_TYPES, type AuditUser } from "./settings/auditEvents";

const NOW_TICK_MS = 60_000;
// Typing commits the search after this pause, so a half-typed query sends no request.
const SEARCH_DEBOUNCE_MS = 300;

const AUDIT_PARAMS = {
	before: pageTokensParser,
	live: parseAsInteger.withDefault(0),
	range: parseAsStringLiteral(RANGES.map((r) => r.value)).withDefault(DEFAULT_RANGE.value),
	from: msParser,
	to: msParser,
	types: parseAsArrayOf(parseAsStringLiteral(AUDIT_EVENT_TYPES)).withDefault([]),
	q: parseAsString.withDefault(""),
	actor: parseAsString.withDefault(""),
	size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
	ws: parseAsString.withDefault(""),
	res: parseAsString.withDefault(""),
	env: parseAsString.withDefault(""),
	event: parseAsString,
};

export function Audit() {
	const { orgId = "" } = useParams<{ orgId: string }>();
	const [params, setParams] = useQueryStates(AUDIT_PARAMS, {
		history: "replace",
	});
	const transport = useTransport();
	const access = useAuditAccess(orgId);
	const { orgs, workspaces, activeWorkspaceId } = useOrgWorkspace();
	const [openedAt, setOpenedAt] = useState(Date.now);
	const now = useNow(NOW_TICK_MS).getTime();

	const { types, q: search, actor, size, ws: wsParam } = params;
	const range = RANGES.find((r) => r.value === params.range) ?? DEFAULT_RANGE;
	const { from, to } = params;
	const custom = from !== null && to !== null && to > from;
	const sent = auditQuery(types, actor, search);
	const choices = access.org ? workspaces.filter((w) => w.orgId === orgId) : access.workspaces;
	// ListEvents reads one workspace per call, so a workspace admin always reads exactly one.
	const workspaceId = choices.some((w) => w.id === wsParam)
		? wsParam
		: access.org
			? ""
			: ((choices.find((w) => w.id === activeWorkspaceId) ?? choices[0])?.id ?? "");
	const selectedDetails = useWorkspaceDetails(workspaceId === "" ? [] : [workspaceId]);
	const location = useLocation();
	const [, setSearchParams] = useSearchParams();
	const checked = auditLink(
		location.search,
		access.isLoading ? undefined : choices.map((w) => w.id),
		selectedDetails.resourcesReady.has(workspaceId)
			? (selectedDetails.resourcesOf.get(workspaceId) ?? []).map((r) => r.id)
			: undefined,
		selectedDetails.environmentsReady.has(workspaceId)
			? (selectedDetails.environmentsOf.get(workspaceId) ?? []).map((e) => e.id)
			: undefined,
		workspaceId,
	);
	const cleanSearch = checked.search;
	const droppedJSON = JSON.stringify(checked.dropped);
	const droppedKey = location.key + droppedJSON;
	const [notice, setNotice] = useState<{ key: string; filters: DroppedFilter[] }>({ key: "", filters: [] });
	if (droppedJSON !== "[]" && notice.key !== droppedKey) {
		setNotice({
			key: droppedKey,
			filters: [
				...notice.filters,
				...checked.dropped.filter(
					(f) => !notice.filters.some((p) => p.label === f.label && p.value === f.value && p.reason === f.reason),
				),
			],
		});
	}
	const dropped = notice.filters;
	useEffect(() => {
		if (droppedJSON !== "[]") setSearchParams(cleanSearch, { replace: true });
	}, [cleanSearch, droppedJSON, setSearchParams]);
	const cleaned = new URLSearchParams(cleanSearch);
	const resourceId = workspaceId === "" ? "" : (cleaned.get("res") ?? "");
	const environmentId = workspaceId === "" ? "" : (cleaned.get("env") ?? "");

	const filterKey = JSON.stringify([
		orgId,
		range.value,
		from,
		to,
		types,
		workspaceId,
		resourceId,
		environmentId,
		size,
		sent,
	]);
	const stack = params.before;
	const live = params.live === 1 && stack.length === 0 && !custom;
	// The search box shows draft while a typed search waits to commit to the q parameter.
	const [draft, setDraft] = useState<string | null>(null);
	const pendingSearchRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const searchRef = useRef<HTMLInputElement>(null);
	useEffect(
		() => () => {
			clearTimeout(pendingSearchRef.current);
		},
		[],
	);
	const setFilters = (next: Partial<{ [K in keyof typeof params]: (typeof params)[K] | null }>) => {
		if ("q" in next) {
			clearTimeout(pendingSearchRef.current);
			setDraft(null);
		}
		void setParams({ ...next, before: null });
	};
	const input = {
		orgId,
		workspaceId,
		resourceId,
		environmentId,
		query: sent,
		pageSize: size,
		startTime: msToTimestamp(custom ? from : openedAt - range.ms),
		...(custom ? { endTime: msToTimestamp(to) } : {}),
	};
	const { query, nextPageToken, total } = useTokenPage(listEvents, input, stack, {
		enabled: orgId !== "" && !access.isLoading && (!(params.res || params.env) || !selectedDetails.isLoading),
		refetchInterval: live ? 5000 : false,
	});
	const members = useTanstackQuery({
		...orgMembersOptions(transport, orgId),
		enabled: access.org,
	});

	const [baseline, setBaseline] = useState<{ key: string; seq: bigint } | null>(null);
	if (query.data !== undefined && baseline?.key !== filterKey) {
		setBaseline({ key: filterKey, seq: query.data.events[0]?.seq ?? 0n });
	}
	const events = query.data?.events ?? [];
	const known = new Set(choices.map((w) => w.id));
	const details = useWorkspaceDetails([
		...new Set(events.map((e) => e.workspaceId).filter((id) => id !== workspaceId && known.has(id))),
	]);
	const users = new Map<string, AuditUser>(
		(members.data?.members ?? []).flatMap((m) => (m.user === undefined ? [] : [[m.user.id, m.user] as const])),
	);
	const names = new Map<string, string>([
		...orgs.map((o): [string, string] => [o.id, o.name]),
		...choices.map((w): [string, string] => [w.id, w.name]),
		...selectedDetails.names,
		...details.names,
	]);
	const rows = events.map((e) => auditRow(e, users, names));
	const actors = new Map<string, { id: string; name: string; detail: string; group: string }>([
		...[...users].map(([id, u]) => [id, { id, name: u.name || u.email, detail: u.email, group: "People" }] as const),
		...rows
			.filter((r) => r.event.actorId !== "" && !TYPE_ACTORS.includes(r.event.actorType))
			.map(
				(r) =>
					[
						r.event.actorId,
						{
							id: r.event.actorId,
							name: r.actor.name,
							detail: r.actor.detail,
							group: r.event.actorType === "user" ? "People" : "Tokens",
						},
					] as const,
			),
		...TYPE_ACTORS.map((t) => [t, { id: t, name: ACTOR_KINDS[t] ?? t, detail: "", group: "Loco" }] as const),
	]);
	if (actor !== "" && !actors.has(actor)) actors.set(actor, { id: actor, name: actor, detail: "", group: "People" });
	const selIdx = rows.findIndex((r) => r.event.id === params.event);
	const selected = rows[selIdx];
	const heldEventId = useHeldWhileClosed(selected?.event.id, selected !== undefined);
	const select = (index: number) => () => {
		void setParams({ event: rows[index]?.event.id ?? null });
	};

	const anyFilter =
		search !== "" ||
		actor !== "" ||
		types.length > 0 ||
		(access.org && workspaceId !== "") ||
		resourceId !== "" ||
		environmentId !== "";
	// Older leaves live mode and the open event; Newer and Newest keep the event open.
	const pager = tokenPager(stack, nextPageToken, (before) => {
		void setParams((before?.length ?? 0) > stack.length ? { before, live: null, event: null } : { before });
	});
	const first = rows[0];
	const last = rows.at(-1);
	const loading = access.isLoading || query.isLoading;
	const rejected = query.error === null ? null : auditSearchError(query.error, types, actor, search);
	const searchRejected = rejected !== null && (draft === null || draft === search);
	const filterRejected = auditFilterViolations(query.error).length > 0;
	const timeWindow: TimeWindow = custom ? { kind: "absolute", from, to } : { kind: "relative", spanMs: range.ms };
	const setWindow = (next: TimeWindow) => {
		const preset = next.kind === "relative" ? RANGES.find((r) => r.ms === next.spanMs) : undefined;
		if (next.kind === "absolute") setFilters({ range: null, from: next.from, to: next.to });
		else if (preset !== undefined) setFilters({ range: preset.value, from: null, to: null });
		else setFilters({ range: null, from: Date.now() - next.spanMs, to: Date.now() });
	};
	const fixFilter = (field: string) => {
		const fix = AUDIT_FILTER_FIXES.get(field);
		if (fix === undefined) return;
		if ("range" in fix.params) setOpenedAt(Date.now());
		setFilters(fix.params);
	};

	return (
		<Page>
			<PageHeader
				title="Audit log"
				actions={
					<>
						<TimeRangePicker
							range={timeWindow}
							setRange={setWindow}
							maxMs={RANGES.at(-1)?.ms ?? DEFAULT_RANGE.ms}
							presets={RANGES.map((r) => r.ms)}
						/>
						<Button
							variant="outline"
							size="sm"
							aria-pressed={live}
							disabled={custom}
							onClick={() => {
								void setParams({ live: live ? null : 1, before: null });
							}}
						>
							{live ? <PauseIcon /> : <PlayIcon />}
							{live ? "Pause" : "Live"}
						</Button>
					</>
				}
			/>
			{query.error !== null && rejected === null && !filterRejected ? (
				<ErrorState error={query.error} what="the audit log" rpc={listEvents} scope="audit" onRetry={query.refetch} />
			) : (
				<Section
					search={
						<SearchInput
							aria-label="Search request ID, actor or target"
							placeholder="Search, or @type:deployment.* @actor:name"
							className="audit-search"
							ref={searchRef}
							aria-invalid={searchRejected}
							aria-describedby={searchRejected ? "audit-query-error" : undefined}
							onClear={() => {
								setFilters({ q: null, event: null });
							}}
							value={draft ?? search}
							onChange={(event) => {
								const text = event.target.value;
								setDraft(text);
								clearTimeout(pendingSearchRef.current);
								pendingSearchRef.current = setTimeout(() => {
									setFilters({ q: text, event: null });
								}, SEARCH_DEBOUNCE_MS);
							}}
						/>
					}
					filters={
						<>
							<EventFilter
								types={types}
								onChange={(next) => {
									setFilters({ types: next, event: null });
								}}
							/>
							{(access.org || choices.length > 1) && (
								<PickFilter
									icon={<LayersIcon />}
									label="Workspace"
									items={choices}
									placeholder="Find a workspace"
									value={workspaceId}
									clearable={access.org}
									onChange={(id) => {
										setFilters({ ws: id, res: null, env: null, event: null });
									}}
								/>
							)}
							{workspaceId !== "" && (
								<>
									<PickFilter
										icon={<BoxIcon />}
										label="Resource"
										items={(selectedDetails.resourcesOf.get(workspaceId) ?? []).map((r) => ({
											id: r.id,
											name: r.name,
										}))}
										value={resourceId}
										placeholder="Find a resource"
										onChange={(id) => {
											setFilters({ res: id, event: null });
										}}
									/>
									<PickFilter
										icon={<GitBranchIcon />}
										label="Environment"
										items={(selectedDetails.environmentsOf.get(workspaceId) ?? []).map((e) => ({
											id: e.id,
											name: e.name,
										}))}
										value={environmentId}
										onChange={(id) => {
											setFilters({ env: id, event: null });
										}}
									/>
								</>
							)}
							<ActorFilter
								items={[...actors.values()]}
								value={actor}
								onChange={(id) => {
									setFilters({ actor: id, event: null });
								}}
							/>
						</>
					}
				>
					{dropped.length > 0 && (
						<DroppedFilters
							filters={dropped}
							onDismiss={() => {
								setNotice({ key: notice.key, filters: [] });
							}}
						/>
					)}
					{filterRejected && query.error !== null && <AuditFilterError error={query.error} onFix={fixFilter} />}
					{searchRejected && (
						<AuditQueryError
							rejected={rejected}
							onField={(field) => {
								const next = insertAuditField(search, rejected.start, rejected.end, field);
								clearTimeout(pendingSearchRef.current);
								setDraft(next.query);
								void setParams({ q: next.query, event: null, before: null }).then(() => {
									setDraft(null);
									requestAnimationFrame(() => {
										searchRef.current?.focus();
										searchRef.current?.setSelectionRange(next.caret, next.caret);
									});
								});
							}}
						/>
					)}
					{!filterRejected && rejected === null && (
						<AuditTable
							rows={rows}
							highlightAfter={live && baseline?.key === filterKey ? baseline.seq : undefined}
							loading={loading}
							selectedId={selected?.event.id ?? null}
							nowMs={now}
							onSelect={(id) => {
								void setParams({ event: id });
							}}
						/>
					)}

					{!loading && !filterRejected && rejected === null && rows.length === 0 && (
						<div className="py-7 text-center text-[13px] text-fg3">
							{anyFilter
								? "No events match these filters."
								: custom
									? "Nothing happened in this range."
									: `Nothing happened in the ${range.label.toLowerCase()}.`}
							{anyFilter && (
								<>
									{" "}
									<Button
										variant="plain-link"
										className="text-[13px]"
										onClick={() => {
											setFilters({
												types: null,
												ws: access.org ? null : wsParam,
												res: null,
												env: null,
												actor: null,
												q: null,
												event: null,
											});
										}}
									>
										Clear filters
									</Button>
								</>
							)}
						</div>
					)}
					{!filterRejected && rejected === null && (
						<Pager
							label={
								first === undefined || last === undefined
									? ""
									: `${tokenPageLabel(stack.length, size, rows.length, total)} events, ${agoLabel(first.ms, now).replace(/ ago$/, "")} to ${agoLabel(last.ms, now)}`
							}
							page={pager.page}
							total={rows.length}
							pageSizes={PAGE_SIZES}
							pageSize={size}
							onPageSize={(n) => {
								setFilters({
									size: PAGE_SIZES.find((s) => s === n) ?? null,
									event: null,
								});
							}}
							firstLabel="Newest events"
							prevLabel="Newer events"
							nextLabel="Older events"
							text={{ first: "Newest", prev: "Newer", next: "Older" }}
							onFirst={pager.onFirst}
							onPrev={pager.onPrev}
							onNext={pager.onNext}
						/>
					)}
				</Section>
			)}
			<AuditDrawer
				open={selected !== undefined}
				row={rows.find((r) => r.event.id === heldEventId)}
				nowMs={now}
				onNewer={selIdx > 0 ? select(selIdx - 1) : null}
				onOlder={selIdx >= 0 && selIdx < rows.length - 1 ? select(selIdx + 1) : null}
				onActor={() => {
					if (selected !== undefined) {
						const e = selected.event;
						setFilters({
							actor: TYPE_ACTORS.includes(e.actorType) ? e.actorType : e.actorId,
							event: null,
						});
					}
				}}
				onResource={
					selected === undefined || selected.event.resourceId === "" || !known.has(selected.event.workspaceId)
						? null
						: () => {
								setFilters({
									ws: selected.event.workspaceId,
									res: selected.event.resourceId,
									env: null,
									event: null,
								});
							}
				}
				onClose={() => {
					void setParams({ event: null });
				}}
			/>
		</Page>
	);
}
