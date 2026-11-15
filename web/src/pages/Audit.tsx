import { useQuery, useTransport } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { keepPreviousData, useQuery as useTanstackQuery } from "@tanstack/react-query";
import { BoxIcon, ChevronDownIcon, ClockIcon, GitBranchIcon, HistoryIcon, LayersIcon, UserIcon } from "lucide-react";
import { useState } from "react";
import { parseAsArrayOf, parseAsNumberLiteral, parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
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

import { AuditDrawer } from "./audit/AuditDrawer";
import { EventFilter } from "./audit/AuditFilters";
import { AuditTable } from "./audit/AuditTable";
import {
	ACTOR_KINDS,
	auditRow,
	DEFAULT_RANGE,
	matchesPageFilter,
	requestIdFilter,
	PAGE_SIZES,
	RANGES,
	TYPE_ACTORS,
} from "./audit/model";
import { useWorkspaceDetails } from "./audit/useWorkspaceDetails";
import { AUDIT_EVENT_TYPES, type AuditUser } from "./settings/auditEvents";

const NOW_TICK_MS = 60_000;

const AUDIT_PARAMS = {
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
	const [params, setParams] = useQueryStates(AUDIT_PARAMS, { history: "replace" });
	const transport = useTransport();
	const access = useAuditAccess(orgId);
	const { orgs, workspaces, activeWorkspaceId } = useOrgWorkspace();
	const [openedAt] = useState(Date.now);
	const now = useNow(NOW_TICK_MS).getTime();

	const { types, q: search, actor, size, ws: wsParam } = params;
	const range = RANGES.find((r) => r.value === params.range) ?? DEFAULT_RANGE;
	const requestId = requestIdFilter(search);
	const actorTypes = TYPE_ACTORS.includes(actor) ? [actor] : [];
	const actorIds = actor !== "" && actorTypes.length === 0 ? [actor] : [];
	const choices = access.org ? workspaces.filter((w) => w.orgId === orgId) : access.workspaces;
	// ListEvents reads one workspace per call, so a workspace admin always reads exactly one.
	const workspaceId = choices.some((w) => w.id === wsParam)
		? wsParam
		: access.org
			? ""
			: ((choices.find((w) => w.id === activeWorkspaceId) ?? choices[0])?.id ?? "");
	const resourceId = workspaceId === "" ? "" : params.res;
	const environmentId = workspaceId === "" ? "" : params.env;

	// The cursor stack belongs to one set of filters; changing any of them starts from the newest page.
	const filterKey = JSON.stringify([
		range.value,
		types,
		workspaceId,
		resourceId,
		environmentId,
		size,
		requestId,
		actor,
	]);
	const [cursor, setCursor] = useState<{ key: string; stack: bigint[] }>({ key: filterKey, stack: [] });
	const stack = cursor.key === filterKey ? cursor.stack : [];
	const query = useQuery(
		listEvents,
		{
			orgId,
			workspaceId,
			resourceId,
			environmentId,
			types,
			requestId,
			actorIds,
			actorTypes,
			pageSize: size,
			beforeSeq: stack.at(-1) ?? 0n,
			startTime: msToTimestamp(openedAt - range.ms),
		},
		{ enabled: orgId !== "" && !access.isLoading, placeholderData: keepPreviousData },
	);
	const members = useTanstackQuery({ ...orgMembersOptions(transport, orgId), enabled: access.org });

	const events = query.data?.events ?? [];
	const known = new Set(choices.map((w) => w.id));
	const details = useWorkspaceDetails([
		...new Set([workspaceId, ...events.map((e) => e.workspaceId)].filter((id) => known.has(id))),
	]);
	const users = new Map<string, AuditUser>(
		(members.data?.members ?? []).flatMap((m) => (m.user === undefined ? [] : [[m.user.id, m.user] as const])),
	);
	const names = new Map<string, string>([
		...orgs.map((o): [string, string] => [o.id, o.name]),
		...choices.map((w): [string, string] => [w.id, w.name]),
		...details.names,
	]);
	const loadedRows = events.map((e) => auditRow(e, users, names));
	const rows = loadedRows.filter((row) => matchesPageFilter(row, search));
	const actors = new Map<string, string>([
		...[...users].map(([id, u]): [string, string] => [id, [u.name, u.email].filter(Boolean).join(" · ")]),
		...loadedRows
			.filter((r) => r.event.actorId !== "" && !TYPE_ACTORS.includes(r.event.actorType))
			.map((r): [string, string] => [r.event.actorId, [r.actor.name, r.actor.detail].filter(Boolean).join(" · ")]),
		...TYPE_ACTORS.map((t): [string, string] => [t, ACTOR_KINDS[t] ?? t]),
	]);
	if (actor !== "" && !actors.has(actor)) actors.set(actor, actor);
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
	const nextBeforeSeq = query.data?.nextBeforeSeq ?? 0n;
	const first = rows[0];
	const last = rows.at(-1);
	const loading = access.isLoading || query.isLoading;

	return (
		<Page>
			<PageHeader
				title="Audit log"
				actions={
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
									void setParams({ range: RANGES.find((r) => r.value === v)?.value ?? null });
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
				}
			/>
			{query.error !== null ? (
				<ErrorState error={query.error} what="the audit log" rpc={listEvents} scope="audit" onRetry={query.refetch} />
			) : (
				<Section
					search={
						<SearchInput
							aria-label="Filter this page"
							placeholder="Filter this page"
							onClear={() => {
								void setParams({ q: null, event: null });
							}}
							value={search}
							onChange={(event) => {
								void setParams({ q: event.target.value, event: null });
							}}
						/>
					}
					filters={
						<>
							<PickFilter
								icon={<UserIcon />}
								label="Actor"
								placeholder="Find a person or token"
								items={[...actors].map(([id, name]) => ({ id, name }))}
								value={actor}
								onChange={(id) => {
									void setParams({ actor: id, event: null });
								}}
							/>
							<EventFilter
								types={types}
								onChange={(next) => {
									void setParams({ types: next, event: null });
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
										void setParams({ ws: id, res: null, env: null, event: null });
									}}
								/>
							)}
							{workspaceId !== "" && (
								<>
									<PickFilter
										icon={<BoxIcon />}
										label="Resource"
										items={details.resourcesOf.get(workspaceId) ?? []}
										value={resourceId}
										placeholder="Find a resource"
										onChange={(id) => {
											void setParams({ res: id, event: null });
										}}
									/>
									<PickFilter
										icon={<GitBranchIcon />}
										label="Environment"
										items={details.environmentsOf.get(workspaceId) ?? []}
										value={environmentId}
										onChange={(id) => {
											void setParams({ env: id, event: null });
										}}
									/>
								</>
							)}
						</>
					}
				>
					<AuditTable
						rows={rows}
						loading={loading}
						selectedId={selected?.event.id ?? null}
						nowMs={now}
						onSelect={(id) => {
							void setParams({ event: id });
						}}
					/>
					{!loading && rows.length === 0 && (
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
											void setParams({ types: null, ws: access.org ? null : wsParam, res: null, env: null });
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
							void setParams({ size: PAGE_SIZES.find((s) => s === n) ?? null, event: null });
						}}
						firstLabel="Newest events"
						prevLabel="Newer events"
						nextLabel="Older events"
						onFirst={
							stack.length > 0
								? () => {
										setCursor({ key: filterKey, stack: [] });
									}
								: null
						}
						onPrev={
							stack.length > 0
								? () => {
										setCursor({ key: filterKey, stack: stack.slice(0, -1) });
									}
								: null
						}
						onNext={
							nextBeforeSeq !== 0n
								? () => {
										setCursor({ key: filterKey, stack: [...stack, nextBeforeSeq] });
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
						void setParams({
							actor: TYPE_ACTORS.includes(e.actorType) ? e.actorType : e.actorId,
							event: null,
						});
					}
				}}
				onResource={
					selected === undefined || selected.event.resourceId === "" || !known.has(selected.event.workspaceId)
						? null
						: () => {
								void setParams({
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
