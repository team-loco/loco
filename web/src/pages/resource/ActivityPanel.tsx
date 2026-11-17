import { useInfiniteQuery } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import type { Event } from "@gen/loco/event/v1/event_pb";
import { ArrowUpRightIcon, KeyRoundIcon } from "lucide-react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useAuditAccess } from "@/hooks/useAuditAccess";
import { getErrorMessage } from "@/lib/error-handler";
import { auditPath } from "@/lib/routes";
import { shortAgoLabel, tsMs } from "@/lib/time";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { auditActor, auditDetail, auditLabel, type AuditUser, distinctAuditEvents } from "@/pages/settings/auditEvents";

const PAGE_SIZE = 10;
const NO_USERS = new Map<string, AuditUser>();
const SKELETON_ROWS = [0, 1, 2];

function ActivityRow({ event, names, nowMs }: { event: Event; names: ReadonlyMap<string, string>; nowMs: number }) {
	const actor = auditActor(event, names);
	const target = auditDetail(event, NO_USERS) ?? "";
	return (
		<li className="flex min-h-[42px] items-center gap-2.5 border-b border-line2 text-[13px] last:border-b-0">
			{event.actorType === "user" ? (
				<MemberAvatar
					name={actor.name}
					email={event.actorEmail}
					avatarUrl=""
					className="size-[22px] [&_[data-slot=avatar-fallback]]:text-[9.5px]"
				/>
			) : (
				<span className="flex size-[22px] shrink-0 items-center justify-center rounded-[6px] border border-line bg-background text-fg2">
					<KeyRoundIcon className="size-3" />
				</span>
			)}
			<span className="min-w-0 flex-1 truncate">
				{actor.name} <span className="text-fg2">{auditLabel(event.type).toLowerCase()}</span>
				{target !== "" && <span className="font-mono text-[12px]"> {target}</span>}
			</span>
			<span className="w-16 shrink-0 text-right font-mono text-[12px] text-fg3">
				{shortAgoLabel(tsMs(event.createdAt), nowMs)}
			</span>
		</li>
	);
}

/** The resource's recent audit events, for anyone who reads the resource. */
export function ActivityPanel({
	orgId,
	workspaceId,
	resourceId,
	environmentId,
	names,
	nowMs,
}: {
	orgId: string;
	workspaceId: string;
	resourceId: string;
	environmentId: string | undefined;
	names: ReadonlyMap<string, string>;
	nowMs: number;
}) {
	const audit = useAuditAccess(orgId);
	const query = useInfiniteQuery(
		listEvents,
		{
			orgId,
			workspaceId,
			resourceId,
			environmentId: environmentId ?? "",
			pageSize: PAGE_SIZE,
			beforeSeq: 0n,
		},
		{
			enabled: environmentId !== undefined,
			pageParamKey: "beforeSeq",
			getNextPageParam: (last) => (last.nextBeforeSeq === 0n ? undefined : last.nextBeforeSeq),
		},
	);
	const events = distinctAuditEvents(query.data?.pages.flatMap((p) => p.events) ?? []);

	return (
		<Section
			title="Activity"
			actions={
				audit.reads(workspaceId) ? (
					<Link
						to={`${auditPath(orgId)}?range=90d&ws=${workspaceId}&res=${resourceId}&env=${environmentId ?? ""}`}
						className="inline-flex items-center gap-1 text-[12.5px] text-fg2 no-underline hover:text-foreground"
					>
						View all in audit log
						<ArrowUpRightIcon className="size-3" />
					</Link>
				) : undefined
			}
			bodyClassName="px-5 py-1.5"
		>
			{query.isLoading &&
				SKELETON_ROWS.map((i) => (
					<div key={i} className="flex h-[42px] items-center">
						<Skeleton className="h-3 w-full" />
					</div>
				))}
			{!query.isLoading && query.error === null && events.length === 0 && (
				<p className="m-0 py-3 text-[13px] text-fg3">No changes yet.</p>
			)}
			<ul aria-label="Activity" className="m-0 list-none p-0">
				{events.map((e) => (
					<ActivityRow key={e.id} event={e} names={names} nowMs={nowMs} />
				))}
			</ul>
			{query.error !== null && (
				<div
					role="alert"
					className="flex flex-wrap items-center gap-3 border-t border-line2 py-2.5 text-[13px] text-fg3"
				>
					<span className="min-w-0 flex-1">
						{getErrorMessage(
							query.error,
							query.isFetchNextPageError ? "Failed to load older activity" : "Failed to load activity",
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
				<div className="border-t border-line2 pt-2.5 pb-1.5" aria-busy={query.isFetchingNextPage}>
					<Button
						variant="plain-link"
						disabled={query.isFetching}
						onClick={() => {
							void query.fetchNextPage();
						}}
					>
						{query.isFetchingNextPage ? "Loading…" : "Show older"}
					</Button>
				</div>
			)}
		</Section>
	);
}
