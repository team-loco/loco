import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { useQueries, useQuery as useTanstackQuery } from "@tanstack/react-query";
import { BoxIcon, BuildingIcon, GitBranchIcon, LayersIcon, PlayIcon, PauseIcon, ServerIcon } from "lucide-react";
import { useEffect, useState } from "react";
import {
	parseAsArrayOf,
	parseAsBoolean,
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
import { ToggleChip } from "@/components/design/ToggleChip";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { orgMembersOptions } from "@/hooks/completeMembers";
import { useAuditAccess } from "@/hooks/useAuditAccess";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { useOrgFilterChoices } from "@/hooks/orgFilterChoices";
import { useWorkspaceFilterChoices } from "@/hooks/orgWorkspaces";
import { useNow } from "@/hooks/useNow";
import { useTokenPage } from "@/hooks/useTokenPage";
import { msParser, type TimeWindow } from "@/lib/obs-time";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { agoLabel, msToTimestamp } from "@/lib/time";
import { TimeRangePicker } from "./observability/Toolbar";
import type { ObsResource } from "./observability/context";
import { QueryErrorLine, QueryInput } from "./observability/QueryBar";
import { QUERY_PARSERS, queryStr, type FieldKey } from "./observability/query";
import { auditSearchError } from "./audit/queryError";

import { AUDIT_FILTER_FIXES, auditLink, auditFilterViolations, type DroppedFilter } from "./audit/link";
import { DroppedFilters, AuditFilterError } from "./audit/AuditFeedback";
import { AuditDrawer } from "./audit/AuditDrawer";
import { ActorFilter, EventFilter } from "./audit/AuditFilters";
import { AuditTable } from "./audit/AuditTable";
import { ACTOR_KINDS, auditQuery, auditRow, DEFAULT_RANGE, PAGE_SIZES, RANGES, TYPE_ACTORS } from "./audit/model";
import { useWorkspaceDetails } from "./audit/useWorkspaceDetails";
import { AUDIT_EVENT_TYPES, type AuditUser } from "./settings/auditEvents";

const NOW_TICK_MS = 60_000;
const NO_RESOURCES = new Map<string, ObsResource>();

const AUDIT_PARAMS = {
	before: pageTokensParser,
	live: parseAsInteger.withDefault(0),
	range: parseAsStringLiteral(RANGES.map((r) => r.value)).withDefault(DEFAULT_RANGE.value),
	from: msParser,
	to: msParser,
	types: parseAsArrayOf(parseAsStringLiteral(AUDIT_EVENT_TYPES)).withDefault([]),
	q: QUERY_PARSERS.q,
	actor: parseAsString.withDefault(""),
	size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
	ws: parseAsString.withDefault(""),
	res: parseAsString.withDefault(""),
	env: parseAsString.withDefault(""),
	event: parseAsString,
	org: parseAsString.withDefault(""),
	installation: parseAsBoolean.withDefault(false),
};

/** An organization's audit log; with `system`, the installation's: every organization's events and the installation's own. */
export function Audit({ system = false }: { system?: boolean }) {
	const { orgId: routeOrgId = "" } = useParams<{ orgId: string }>();
	const [params, setParams] = useQueryStates(AUDIT_PARAMS, {
		history: "replace",
	});
	const transport = useTransport();
	const access = useAuditAccess(system ? null : routeOrgId);
	const { orgs, activeWorkspaceId } = useOrgWorkspace();
	const [openedAt, setOpenedAt] = useState(Date.now);
	const now = useNow(NOW_TICK_MS).getTime();

	const { types, actor, size, ws: wsParam } = params;
	const search = queryStr(params.q.tokens, params.q.text);
	const range = RANGES.find((r) => r.value === params.range) ?? DEFAULT_RANGE;
	const { from, to } = params;
	const custom = from !== null && to !== null && to > from;
	const sent = auditQuery(types, actor, search);
	const orgChoices = useWorkspaceFilterChoices(routeOrgId, wsParam, access.org);
	const choices = access.org ? orgChoices.workspaces : access.workspaces;
	const choicesLoading = access.isLoading || orgChoices.selectedLoading;
	// ListEvents reads one workspace per call, so a workspace admin always reads exactly one.
	const workspaceId = choices.some((w) => w.id === wsParam)
		? wsParam
		: access.org
			? ""
			: ((choices.find((w) => w.id === activeWorkspaceId) ?? choices[0])?.id ?? "");
	const selectedDetails = useWorkspaceDetails(workspaceId === "" ? [] : [workspaceId]);
	const location = useLocation();
	const [, setSearchParams] = useSearchParams();
	const filtersReady = !choicesLoading;
	const resourcesReady = selectedDetails.resourcesReady.has(workspaceId);
	const environmentsReady = selectedDetails.environmentsReady.has(workspaceId);
	const checked = auditLink(
		location.search,
		filtersReady ? choices.map((w) => w.id) : undefined,
		resourcesReady ? (selectedDetails.resourcesOf.get(workspaceId) ?? []).map((r) => r.id) : undefined,
		environmentsReady ? (selectedDetails.environmentsOf.get(workspaceId) ?? []).map((e) => e.id) : undefined,
		workspaceId,
		system,
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
	// The System log reads the organization the link names, or only installation events; an organization's log reads its own.
	const orgId = system ? (cleaned.get("org") ?? "") : routeOrgId;
	const installationOnly = system && cleaned.get("installation") === "true";

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
		installationOnly,
	]);
	const stack = params.before;
	const live = params.live === 1 && stack.length === 0 && !custom;
	const setFilters = (next: Partial<{ [K in keyof typeof params]: (typeof params)[K] | null }>) => {
		void setParams({ ...next, before: null });
	};
	const input = {
		orgId,
		workspaceId,
		resourceId,
		environmentId,
		query: sent,
		installationOnly,
		pageSize: size,
		startTime: msToTimestamp(custom ? from : openedAt - range.ms),
		...(custom ? { endTime: msToTimestamp(to) } : {}),
	};
	const enabled =
		(system || orgId !== "") && !choicesLoading && (!(params.res || params.env) || !selectedDetails.isLoading);
	const { query, nextPageToken, total } = useTokenPage(listEvents, input, stack, {
		enabled,
		refetchInterval: live ? 5000 : false,
	});
	const facets = useQuery(listEvents, { ...input, query: "", pageSize: 1, includeFacets: true }, { enabled });
	// Loco's own events match @actor_type:system, not @actor:system.
	const valueCounts = (key: FieldKey): [string, number][] =>
		(facets.data?.facets.find((f) => f.key === key)?.values ?? [])
			.filter((v) => key !== "actor" || v.value !== "system")
			.map((v) => [v.value, v.count]);
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
	const eventWorkspaceIds = [
		...new Set(events.map((e) => e.workspaceId).filter((id) => id !== "" && (access.org || known.has(id)))),
	];
	const eventWorkspaces = useQueries({
		queries: eventWorkspaceIds
			.filter((id) => !known.has(id))
			.map((id) => createQueryOptions(getWorkspace, { workspaceId: id }, { transport })),
	});
	for (const q of eventWorkspaces) if (q.data?.workspace !== undefined) known.add(q.data.workspace.id);
	const details = useWorkspaceDetails(eventWorkspaceIds.filter((id) => id !== workspaceId && known.has(id)));
	const users = new Map<string, AuditUser>(
		(members.data?.members ?? []).flatMap((m) => (m.user === undefined ? [] : [[m.user.id, m.user] as const])),
	);
	const names = new Map<string, string>([
		...orgs.map((o): [string, string] => [o.id, o.name]),
		...choices.map((w): [string, string] => [w.id, w.name]),
		...eventWorkspaces.flatMap((q): [string, string][] =>
			q.data?.workspace === undefined ? [] : [[q.data.workspace.id, q.data.workspace.name]],
		),
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
	const orgChoice = useOrgFilterChoices(system);
	const orgItems = orgChoice.orgs.map((o) => ({ id: o.id, name: o.name }));
	if (orgId !== "" && !orgItems.some((o) => o.id === orgId)) {
		const name = rows.find((r) => r.event.orgId === orgId)?.org ?? names.get(orgId) ?? orgId.slice(-8);
		orgItems.unshift({ id: orgId, name });
	}
	const selIdx = rows.findIndex((r) => r.event.id === params.event);
	const selected = rows[selIdx];
	const heldEventId = useHeldWhileClosed(selected?.event.id, selected !== undefined);
	const select = (index: number) => () => {
		void setParams({ event: rows[index]?.event.id ?? null });
	};

	const searchFiltered = search !== "" || actor !== "" || types.length > 0;
	const orgScoped = (access.org && workspaceId !== "") || (system && orgId !== "") || installationOnly;
	const resourceScoped = resourceId !== "" || environmentId !== "";
	const anyFilter = searchFiltered || orgScoped || resourceScoped;
	// Older leaves live mode and the open event; Newer and Newest keep the event open.
	const pager = tokenPager(stack, nextPageToken, (before) => {
		void setParams((before?.length ?? 0) > stack.length ? { before, live: null, event: null } : { before });
	});
	const first = rows[0];
	const last = rows.at(-1);
	const loading = choicesLoading || query.isLoading;
	const rejected = query.error === null ? null : auditSearchError(query.error, types, actor, search);
	const filterRejected = auditFilterViolations(query.error).length > 0;
	const loadError = rejected === null && !filterRejected ? query.error : null;
	const showRows = !filterRejected && rejected === null && loadError === null;
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
			{loadError !== null && (
				<ErrorState error={loadError} what="the audit log" rpc={listEvents} scope="audit" onRetry={query.refetch} />
			)}
			<div className="flex flex-col gap-2">
				<QueryInput
					valueCounts={valueCounts}
					state={{
						orgId,
						workspaceId,
						view: "audit",
						tokens: params.q.tokens,
						text: params.q.text,
						setQuery: (tokens, text) => {
							setFilters({ q: { tokens, text }, event: null });
						},
						resourceByName: NO_RESOURCES,
					}}
				/>
				{rejected !== null && <QueryErrorLine error={rejected.error} highlightTo={rejected.end} />}
			</div>
			<Section
				filters={
					<>
						<EventFilter
							types={types}
							onChange={(next) => {
								setFilters({ types: next, event: null });
							}}
						/>
						{system && (
							<>
								<PickFilter
									icon={<BuildingIcon />}
									label="Organization"
									items={orgItems}
									placeholder="Find an organization"
									value={orgId}
									onSearch={orgChoice.onSearch}
									onChange={(id) => {
										setFilters({ org: id, installation: null, event: null });
									}}
								/>
								<ToggleChip
									variant="button"
									pressed={installationOnly}
									onPressedChange={(on) => {
										setFilters({ installation: on ? true : null, org: null, event: null });
									}}
								>
									<ServerIcon />
									Installation only
								</ToggleChip>
							</>
						)}
						{(access.org || choices.length > 1) && (
							<PickFilter
								icon={<LayersIcon />}
								label="Workspace"
								items={choices}
								placeholder="Find a workspace"
								value={workspaceId}
								clearable={access.org}
								{...(access.org ? { onSearch: orgChoices.onSearch } : {})}
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
				{showRows && (
					<AuditTable
						rows={rows}
						highlightAfter={live && baseline?.key === filterKey ? baseline.seq : undefined}
						loading={loading}
						selectedId={selected?.event.id ?? null}
						nowMs={now}
						showOrg={system}
						onSelect={(id) => {
							void setParams({ event: id });
						}}
					/>
				)}

				{!loading && showRows && rows.length === 0 && (
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
											org: null,
											installation: null,
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
				{showRows && (
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
						loading={loading}
						onFirst={pager.onFirst}
						onPrev={pager.onPrev}
						onNext={pager.onNext}
					/>
				)}
			</Section>
			<AuditDrawer
				open={selected !== undefined}
				showOrg={system}
				row={rows.find((r) => r.event.id === heldEventId)}
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
