import { useQuery, useTransport } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { keepPreviousData, useQuery as useTanstackQuery } from "@tanstack/react-query";
import {
	BoxIcon,
	ChevronDownIcon,
	ClockIcon,
	GitBranchIcon,
	HistoryIcon,
	LayersIcon,
	PlayIcon,
	PauseIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import {
	createParser,
	parseAsArrayOf,
	parseAsInteger,
	parseAsNumberLiteral,
	parseAsString,
	parseAsStringLiteral,
	useQueryStates,
} from "nuqs";
import { useParams } from "react-router";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { EmptyState } from "@/components/design/EmptyState";
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
import { agoLabel, msToTimestamp } from "@/lib/time";
import { queryError } from "@/pages/observability/query";
import { QueryErrorLine } from "@/pages/observability/QueryBar";

import { AuditDrawer } from "./audit/AuditDrawer";
import { ActorFilter, EventFilter } from "./audit/AuditFilters";
import { AuditTable } from "./audit/AuditTable";
import { ACTOR_KINDS, auditQuery, auditRow, DEFAULT_RANGE, PAGE_SIZES, RANGES, TYPE_ACTORS } from "./audit/model";
import { useWorkspaceDetails } from "./audit/useWorkspaceDetails";
import { AUDIT_EVENT_TYPES, type AuditUser } from "./settings/auditEvents";

const NOW_TICK_MS = 60_000;
// Typing commits the search after this pause, so a half-typed query sends no request.
const SEARCH_DEBOUNCE_MS = 300;

const sequenceParser = createParser<bigint>({
	parse: (value) =>
		/^\d+$/.test(value) && BigInt(value) > 0n && BigInt(value) <= 9223372036854775807n ? BigInt(value) : null,
	serialize: (value) => value.toString(),
});
const AUDIT_PARAMS = {
	before: parseAsArrayOf(sequenceParser).withDefault([]),
	live: parseAsInteger.withDefault(0),
	range: parseAsStringLiteral(RANGES.map((r) => r.value)).withDefault(DEFAULT_RANGE.value),
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
	const [openedAt] = useState(Date.now);
	const now = useNow(NOW_TICK_MS).getTime();

	const { types, q: search, actor, size, ws: wsParam } = params;
	const range = RANGES.find((r) => r.value === params.range) ?? DEFAULT_RANGE;
	const sent = auditQuery(types, actor, search);
	const choices = access.org ? workspaces.filter((w) => w.orgId === orgId) : access.workspaces;
	// ListEvents reads one workspace per call, so a workspace admin always reads exactly one.
	const workspaceId = choices.some((w) => w.id === wsParam)
		? wsParam
		: access.org
			? ""
			: ((choices.find((w) => w.id === activeWorkspaceId) ?? choices[0])?.id ?? "");
	const resourceId = workspaceId === "" ? "" : params.res;
	const environmentId = workspaceId === "" ? "" : params.env;
	const environmentName = workspaceId === "" ? params.env : "";

	const filterKey = JSON.stringify([
		orgId,
		range.value,
		types,
		workspaceId,
		resourceId,
		environmentId,
		environmentName,
		size,
		sent,
	]);
	const stack = params.before;
	const live = params.live === 1 && stack.length === 0;
	// The search box shows draft while a typed search waits to commit to the q parameter.
	const [draft, setDraft] = useState<string | null>(null);
	const pendingSearchRef = useRef<ReturnType<typeof setTimeout>>(undefined);
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
	const query = useQuery(
		listEvents,
		{
			orgId,
			workspaceId,
			resourceId,
			environmentId,
			environmentName,
			query: sent,
			pageSize: size,
			beforeSeq: stack.at(-1) ?? 0n,
			startTime: msToTimestamp(openedAt - range.ms),
		},
		{
			enabled: orgId !== "" && !access.isLoading,
			placeholderData: keepPreviousData,
			refetchInterval: live ? 5000 : false,
		},
	);
	const members = useTanstackQuery({
		...orgMembersOptions(transport, orgId),
		enabled: access.org,
	});

	const [baseline, setBaseline] = useState<{ key: string; seq: bigint } | null>(null);
	if (query.data !== undefined && !query.isPlaceholderData && baseline?.key !== filterKey) {
		setBaseline({ key: filterKey, seq: query.data.events[0]?.seq ?? 0n });
	}
	const events = query.data?.events ?? [];
	const known = new Set(choices.map((w) => w.id));
	const details = useWorkspaceDetails(choices.map((w) => w.id));
	const environmentNames = [...new Set([...details.environmentsOf.values()].flat().map((e) => e.name))]
		.toSorted()
		.map((name) => ({ id: name, name }));
	const users = new Map<string, AuditUser>(
		(members.data?.members ?? []).flatMap((m) => (m.user === undefined ? [] : [[m.user.id, m.user] as const])),
	);
	const names = new Map<string, string>([
		...orgs.map((o): [string, string] => [o.id, o.name]),
		...choices.map((w): [string, string] => [w.id, w.name]),
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
		environmentId !== "" ||
		environmentName !== "";
	const nextBeforeSeq = query.data?.nextBeforeSeq ?? 0n;
	const first = rows[0];
	const last = rows.at(-1);
	const loading = access.isLoading || query.isLoading;
	const rejected = query.error === null ? null : queryError(query.error, sent);

	return (
		<Page>
			<PageHeader
				title="Audit log"
				actions={
					<>
						<Button
							variant="outline"
							aria-pressed={live}
							onClick={() => {
								void setParams({ live: live ? null : 1, before: null });
							}}
						>
							{live ? <PauseIcon /> : <PlayIcon />}
							{live ? "Pause" : "Live"}
						</Button>
						<DropdownMenu>
							<DropdownMenuTrigger render={<Button variant="outline" className="px-2.5" />}>
								<ClockIcon className="text-fg3" />
								{range.label}
								<ChevronDownIcon className="size-3 text-fg3" />
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="w-[200px]">
								<DropdownMenuRadioGroup
									value={range.value}
									onValueChange={(v: string) => {
										setFilters({
											range: RANGES.find((r) => r.value === v)?.value ?? null,
										});
									}}
								>
									{RANGES.map((r) => (
										<DropdownMenuRadioItem key={r.value} value={r.value}>
											{r.label}
										</DropdownMenuRadioItem>
									))}
								</DropdownMenuRadioGroup>
							</DropdownMenuContent>
						</DropdownMenu>
					</>
				}
			/>
			{query.error !== null && rejected === null ? (
				<ErrorState error={query.error} what="the audit log" rpc={listEvents} scope="audit" onRetry={query.refetch} />
			) : (
				<Section
					search={
						<SearchInput
							aria-label="Search request ID, actor or target"
							placeholder="Search request ID, actor or target"
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
								<PickFilter
									icon={<BoxIcon />}
									label="Resource"
									items={details.resourcesOf.get(workspaceId) ?? []}
									value={resourceId}
									placeholder="Find a resource"
									onChange={(id) => {
										setFilters({ res: id, event: null });
									}}
								/>
							)}
							<PickFilter
								icon={<GitBranchIcon />}
								label="Environment"
								items={workspaceId === "" ? environmentNames : (details.environmentsOf.get(workspaceId) ?? [])}
								value={environmentId || environmentName}
								onChange={(id) => {
									setFilters({ env: id, event: null });
								}}
							/>
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
					{rejected !== null && <QueryErrorLine error={rejected} />}
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
					{!loading && rejected === null && rows.length === 0 && (
						<EmptyState
							icon={<HistoryIcon />}
							title={
								anyFilter ? "No events match these filters" : `Nothing happened in the ${range.label.toLowerCase()}`
							}
							action={
								anyFilter ? (
									<Button
										variant="outline"
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
								) : undefined
							}
						/>
					)}
					<Pager
						label={
							first === undefined || last === undefined
								? ""
								: `${rows.length.toLocaleString()} events, ${agoLabel(first.ms, now)} to ${agoLabel(last.ms, now)}`
						}
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
						onFirst={
							stack.length > 0
								? () => {
										void setParams({ before: null });
									}
								: null
						}
						onPrev={
							stack.length > 0
								? () => {
										void setParams({ before: stack.slice(0, -1) });
									}
								: null
						}
						onNext={
							nextBeforeSeq !== 0n
								? () => {
										void setParams({
											before: [...stack, nextBeforeSeq],
											live: null,
											event: null,
										});
									}
								: null
						}
					/>
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
