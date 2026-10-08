import { useInfiniteQuery } from "@connectrpc/connect-query";
import { listOrgEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import type { Event } from "@gen/loco/event/v1/event_pb";
import type { Organization, User } from "@gen/loco/org/v1/org_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { ScrollTextIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Skeleton } from "@/components/design/Skeleton";
import { getErrorMessage } from "@/lib/error-handler";
import { formatClock, maybeTsMs } from "@/lib/time";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import {
	AUDIT_CATEGORIES,
	type AuditCategory,
	auditActor,
	auditDetail,
	auditLabel,
	type AuditUser,
	typesIn,
} from "./auditEvents";
import { formatDay, SettingsCard } from "./parts";

const PAGE_SIZE = 50;

function EventRow({
	event,
	users,
	names,
}: {
	event: Event;
	users: ReadonlyMap<string, AuditUser>;
	names: ReadonlyMap<string, string>;
}) {
	const ms = maybeTsMs(event.createdAt);
	const actor = auditActor(event, names);
	const detail = auditDetail(event, users);
	return (
		<div className="flex flex-col gap-1.5 border-b border-line px-5 py-3 last:border-b-0 md:flex-row md:items-center md:gap-4">
			<div className="flex min-w-0 items-center gap-2.5 md:w-[240px] md:shrink-0">
				<MemberAvatar name={actor.name} email={event.actorEmail} avatarUrl="" className="size-6" />
				<div className="flex min-w-0 flex-col">
					<span className="truncate text-[13.5px] font-medium">{actor.name}</span>
					{actor.detail === "" ? null : <span className="truncate text-[12px] text-fg3">{actor.detail}</span>}
				</div>
			</div>
			<div className="flex min-w-0 flex-1 flex-col">
				<span className="text-[13.5px]">{auditLabel(event.type)}</span>
				{detail !== undefined && detail !== "" && (
					<span className="truncate font-mono text-[12px] text-fg3">{detail}</span>
				)}
			</div>
			<span className="shrink-0 text-[12.5px] text-fg3 tabular-nums md:text-right">
				{ms === undefined ? "—" : `${formatDay(event.createdAt)} · ${formatClock(ms)}`}
			</span>
		</div>
	);
}

export function AuditLogTab({
	org,
	workspaces,
	users,
}: {
	org: Organization;
	workspaces: Workspace[];
	users: User[];
}) {
	const orgId = org.id;
	const usersById = new Map<string, AuditUser>(users.map((u) => [u.id, { name: u.name, email: u.email }]));
	const names = new Map<string, string>([[org.id, org.name], ...workspaces.map((w): [string, string] => [w.id, w.name])]);
	const [category, setCategory] = useState<AuditCategory | "all">("all");
	const query = useInfiniteQuery(
		listOrgEvents,
		{ orgId, types: typesIn(category), pageSize: PAGE_SIZE, beforeSeq: 0n },
		{
			pageParamKey: "beforeSeq",
			getNextPageParam: (last) => (last.nextBeforeSeq === 0n ? undefined : last.nextBeforeSeq),
		},
	);
	const events = query.data?.pages.flatMap((p) => p.events) ?? [];

	return (
		<SettingsCard>
			<div className="flex flex-wrap items-start gap-3 border-b border-line px-5 py-3.5">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<span className="font-semibold">Audit log</span>
					<span className="text-[12.5px] text-fg3">
						Changes made in this organization, newest first. Only organization admins can see this.
					</span>
				</div>
				<FilterMenu
					label="Category"
					value={category}
					options={AUDIT_CATEGORIES}
					onChange={(v) => {
						const next = AUDIT_CATEGORIES.find((c) => c.value === v);
						if (next !== undefined) setCategory(next.value);
					}}
				/>
			</div>
			{query.isLoading && (
				<div className="flex flex-col gap-2 px-5 py-3">
					<Skeleton className="h-9 w-full" />
					<Skeleton className="h-9 w-full" />
					<Skeleton className="h-9 w-full" />
				</div>
			)}
			{query.error !== null && (
				<div className="px-5 py-5 text-fg3">{getErrorMessage(query.error, "Failed to load the audit log")}</div>
			)}
			{!query.isLoading && query.error === null && events.length === 0 && (
				<EmptyState icon={<ScrollTextIcon />} title="No events yet">
					Changes will show up here as they happen.
				</EmptyState>
			)}
			{events.map((e) => (
				<EventRow key={e.id} event={e} users={usersById} names={names} />
			))}
			{query.hasNextPage && (
				<div className="border-t border-line bg-bg2 px-5 py-3">
					<Button
						variant="outline"
						className="h-[30px] px-2.5"
						disabled={query.isFetchingNextPage}
						onClick={() => {
							void query.fetchNextPage();
						}}
					>
						{query.isFetchingNextPage ? "Loading…" : "Load more"}
					</Button>
				</div>
			)}
		</SettingsCard>
	);
}
