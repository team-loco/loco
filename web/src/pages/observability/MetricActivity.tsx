import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { ArrowUpRightIcon } from "lucide-react";
import { Button } from "@/components/design/Button";
import { ErrorState } from "@/components/design/ErrorState";
import { Skeleton } from "@/components/design/Skeleton";
import { ACTIVITY_EVENT_TYPES, runtimeEvent } from "@/lib/runtime-events";
import { formatHourMinute, msToTimestamp, tsMs } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { eventSummary } from "./events";
import { quoteValue } from "./query";
import { useObs } from "./context";

export function MetricActivity({
	resourceId,
	regions,
	from,
	to,
}: {
	resourceId: string;
	regions: string[];
	from: number;
	to: number;
}) {
	const { orgId, workspaceId, environmentId, range, goTo } = useObs();
	const transport = useTransport();
	const timePref = useTimePreferences();
	const query = useQuery({
		...createQueryOptions(
			listEvents,
			{
				orgId,
				workspaceId,
				environmentId,
				resourceId,
				startTime: msToTimestamp(from),
				endTime: msToTimestamp(to),
				pageSize: 5,
				types: [
					...ACTIVITY_EVENT_TYPES,
					"deployment.created",
					"deployment.ready",
					"deployment.failed",
					"deployment.crashed",
				],
				query: regions.map((region) => `@region:${quoteValue(region)}`).join(" OR "),
			},
			{ transport },
		),
		refetchInterval: range.kind === "relative" ? 60000 : false,
	});
	let content;
	if (query.error !== null)
		content = <ErrorState error={query.error} what="replica activity" rpc={listEvents} onRetry={query.refetch} />;
	else if (query.isLoading) content = <Skeleton className="h-32" />;
	else if (query.data?.events.length === 0)
		content = <p className="my-auto py-8 text-[12px] text-fg3">No replica activity in this range</p>;
	else
		content = (
			<ul className="m-0 list-none divide-y divide-line2 p-0">
				{query.data?.events.map((event) => {
					const detail = eventSummary(runtimeEvent(event));
					return (
						<li key={event.id}>
							<button
								type="button"
								className="flex w-full items-start gap-3 py-3 text-left text-[12px]"
								onClick={() => {
									goTo("events");
								}}
							>
								<time className="shrink-0 font-mono text-[11px] text-fg3">
									{formatHourMinute(tsMs(event.createdAt), timePref)}
								</time>
								<span className="min-w-0 flex-1">
									<span className="block">{event.title}</span>
									{detail !== "" && <span className="mt-0.5 block break-words text-[11px] text-fg3">{detail}</span>}
								</span>
								<ArrowUpRightIcon className="mt-0.5 size-3 shrink-0 text-fg3" />
							</button>
						</li>
					);
				})}
			</ul>
		);
	return (
		<section
			aria-label="Replica activity"
			className="flex min-w-0 flex-col gap-2 rounded-card bg-panel px-5 pt-4 pb-3.5"
		>
			<div className="flex items-center justify-between gap-2">
				<h3 className="m-0 text-[14px] font-medium">Replica activity</h3>
				<Button
					variant="link"
					onClick={() => {
						goTo("events");
					}}
				>
					View events
				</Button>
			</div>
			{content}
		</section>
	);
}
