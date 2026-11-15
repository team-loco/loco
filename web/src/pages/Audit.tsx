import { useQuery, useTransport } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { keepPreviousData, useQuery as useTanstackQuery } from "@tanstack/react-query";
import { BoxIcon, ChevronDownIcon, ClockIcon, GitBranchIcon, HistoryIcon, LayersIcon, UserIcon } from "lucide-react";
import { useState } from "react";
import { useParams, useSearchParams } from "react-router";

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

export function Audit() {
	const { orgId = "" } = useParams<{ orgId: string }>();
	const [params, setParams] = useSearchParams();
	const transport = useTransport();
	const access = useAuditAccess(orgId);
	const { orgs, workspaces, activeWorkspaceId } = useOrgWorkspace();
	const [openedAt] = useState(Date.now);
	const now = useNow(NOW_TICK_MS).getTime();

	const update = (patch: Record<string, string | null>) => {
		setParams(
			(prev) => {
				const next = new URLSearchParams(prev);
				for (const [k, v] of Object.entries(patch)) {
					if (v === null || v === "") next.delete(k);
					else next.set(k, v);
				}
				return next;
			},
			{ replace: true },
		);
	};

	const range = RANGES.find((r) => r.value === params.get("range")) ?? DEFAULT_RANGE;
	const types = (params.get("types") ?? "").split(",").filter((t) => AUDIT_EVENT_TYPES.includes(t));
	const search = params.get("q") ?? "";
	const requestId = requestIdFilter(search);
	const actor = params.get("actor") ?? "";
	const actorTypes = TYPE_ACTORS.includes(actor) ? [actor] : [];
	const actorIds = actor !== "" && actorTypes.length === 0 ? [actor] : [];
	const size = PAGE_SIZES.find((n) => n.toString() === params.get("size")) ?? PAGE_SIZES[0];
	const choices = access.org ? workspaces.filter((w) => w.orgId === orgId) : access.workspaces;
	const wsParam = params.get("ws") ?? "";
	// ListEvents reads one workspace per call, so a workspace admin always reads exactly one.
	const workspaceId = choices.some((w) => w.id === wsParam)
		? wsParam
		: access.org
			? ""
			: ((choices.find((w) => w.id === activeWorkspaceId) ?? choices[0])?.id ?? "");
	const resourceId = workspaceId === "" ? "" : (params.get("res") ?? "");
	const environmentId = workspaceId === "" ? "" : (params.get("env") ?? "");

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
	const selIdx = rows.findIndex((r) => r.event.id === params.get("event"));
	const selected = rows[selIdx];
	const heldEventId = useHeldWhileClosed(selected?.event.id, selected !== undefined);
	const select = (index: number) => () => {
		update({ event: rows[index]?.event.id ?? null });
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
									update({ range: v === DEFAULT_RANGE.value ? null : v });
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
								update({ q: null, event: null });
							}}
							value={search}
							onChange={(event) => {
								update({ q: event.target.value, event: null });
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
									update({ actor: id, event: null });
								}}
							/>
							<EventFilter
								types={types}
								onChange={(next) => {
									update({ types: next.join(","), event: null });
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
										update({ ws: id, res: null, env: null, event: null });
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
											update({ res: id, event: null });
										}}
									/>
									<PickFilter
										icon={<GitBranchIcon />}
										label="Environment"
										items={details.environmentsOf.get(workspaceId) ?? []}
										value={environmentId}
										onChange={(id) => {
											update({ env: id, event: null });
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
							update({ event: id });
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
											update({ types: null, ws: access.org ? null : wsParam, res: null, env: null });
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
							update({ size: n === PAGE_SIZES[0] ? null : n.toString(), event: null });
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
						update({
							actor: TYPE_ACTORS.includes(e.actorType) ? e.actorType : e.actorId,
							event: null,
						});
					}
				}}
				onResource={
					selected === undefined || selected.event.resourceId === "" || !known.has(selected.event.workspaceId)
						? null
						: () => {
								update({ ws: selected.event.workspaceId, res: selected.event.resourceId, env: null, event: null });
							}
				}
				onClose={() => {
					update({ event: null });
				}}
			/>
		</Page>
	);
}
