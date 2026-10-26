import { useInfiniteQuery } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import type { Event } from "@gen/loco/event/v1/event_pb";
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
	distinctAuditEvents,
} from "./auditEvents";
import { formatDay, SettingsCard } from "./parts";

const PAGE_SIZE = 50;
const NO_USERS = new Map<string, AuditUser>();
const NO_NAMES = new Map<string, string>();

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
		<li className="flex flex-col gap-1.5 border-b border-line px-5 py-3 last:border-b-0 md:flex-row md:items-center md:gap-4">
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
			<time
				className="shrink-0 text-[12.5px] text-fg3 tabular-nums md:text-right"
				dateTime={ms === undefined ? undefined : new Date(ms).toISOString()}
				aria-label={ms === undefined ? undefined : new Date(ms).toLocaleString(undefined, { timeZoneName: "short" })}
			>
				{ms === undefined ? "—" : `${formatDay(event.createdAt)} · ${formatClock(ms)}`}
			</time>
		</li>
	);
}

export function ActivityPanel({
	orgId,
	workspaceId = "",
	resourceId = "",
	title = "Activity",
	description,
	users = NO_USERS,
	names = NO_NAMES,
}: {
	orgId: string;
	workspaceId?: string;
	resourceId?: string;
	title?: string;
	description: string;
	users?: ReadonlyMap<string, AuditUser>;
	names?: ReadonlyMap<string, string>;
}) {
	const [category, setCategory] = useState<AuditCategory | "all">("all");
	const query = useInfiniteQuery(
		listEvents,
		{ orgId, workspaceId, resourceId, types: typesIn(category), pageSize: PAGE_SIZE, beforeSeq: 0n },
		{
			pageParamKey: "beforeSeq",
			getNextPageParam: (last) => (last.nextBeforeSeq === 0n ? undefined : last.nextBeforeSeq),
		},
	);
	const events = distinctAuditEvents(query.data?.pages.flatMap((p) => p.events) ?? []);

	return (
		<SettingsCard>
			<div className="flex flex-wrap items-start gap-3 border-b border-line px-5 py-3.5">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<h2 className="font-semibold">{title}</h2>
					<span className="text-[12.5px] text-fg3">{description}</span>
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
			{query.error !== null && events.length === 0 && (
				<div role="alert" className="flex flex-wrap items-center gap-3 px-5 py-5 text-fg3">
					<span className="min-w-0 flex-1">{getErrorMessage(query.error, "Failed to load activity")}</span>
					<Button
						variant="outline"
						disabled={query.isFetching}
						aria-busy={query.isFetching}
						onClick={() => {
							void query.refetch();
						}}
					>
						Retry
					</Button>
				</div>
			)}
			{!query.isLoading && query.error === null && events.length === 0 && (
				<EmptyState
					icon={<ScrollTextIcon />}
					title={category === "all" ? "No events yet" : "No matching activity"}
					action={
						category === "all" ? undefined : (
							<Button
								variant="outline"
								onClick={() => {
									setCategory("all");
								}}
							>
								Clear filter
							</Button>
						)
					}
				>
					{category === "all" ? "Changes will show up here as they happen." : "No events match this category."}
				</EmptyState>
			)}
			<ul aria-label={title}>
				{events.map((e) => (
					<EventRow key={e.id} event={e} users={users} names={names} />
				))}
			</ul>
			{query.error !== null && events.length > 0 && (
				<div role="alert" className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3 text-fg3">
					<span className="min-w-0 flex-1">
						{getErrorMessage(
							query.error,
							query.isFetchNextPageError ? "Failed to load older activity" : "Failed to refresh activity",
						)}
					</span>
					<Button
						variant="outline"
						disabled={query.isFetching}
						aria-busy={query.isFetching}
						onClick={() => {
							if (query.isFetchNextPageError) void query.fetchNextPage();
							else void query.refetch();
						}}
					>
						Retry
					</Button>
				</div>
			)}
			{query.hasNextPage && !query.isFetchNextPageError && (
				<div className="border-t border-line bg-bg2 px-5 py-3" aria-busy={query.isFetchingNextPage}>
					<Button
						variant="outline"
						className="h-[30px] px-2.5"
						disabled={query.isFetching}
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
