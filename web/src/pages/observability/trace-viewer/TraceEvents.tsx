import { Button } from "@/components/design/Button";
import { Spinner } from "@/components/design/Spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useWorkspaceEvents } from "@/hooks/useWorkspaceEvents";
import { getErrorMessage } from "@/lib/error-handler";
import { ENVIRONMENT_EVENT_TYPES } from "@/lib/runtime-events";
import { formatClock, shortDurationLabel } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { useObs } from "../context";
import { EventIcon } from "../EventDetail";
import { eventItem } from "../events";
import type { TraceSummary } from "../traces";
import { useTraceResources } from "./TraceColors";
import { traceBounds } from "./trace-time";

const WINDOWS = [
	{ value: 5, label: "±5m" },
	{ value: 10, label: "±10m" },
	{ value: 30, label: "±30m" },
	{ value: 60, label: "±1h" },
];

export function WindowToggle({
	label,
	minutes,
	onMinutes,
}: {
	label: string;
	minutes: number;
	onMinutes: (minutes: number) => void;
}) {
	return (
		<ToggleGroup
			variant="segmented"
			aria-label={label}
			value={[String(minutes)]}
			onValueChange={(values) => {
				const next = WINDOWS.find((window) => String(window.value) === values[0]);
				if (next !== undefined) onMinutes(next.value);
			}}
		>
			{WINDOWS.map((window) => (
				<ToggleGroupItem key={window.value} value={String(window.value)}>
					{window.label}
				</ToggleGroupItem>
			))}
		</ToggleGroup>
	);
}

/** Events of the trace's resources within `minutes` of the trace. */
export function useTraceEvents(trace: TraceSummary, minutes: number) {
	const { workspaceId, installation, resourceById } = useObs();
	const resources = [...new Set(trace.spans.map((span) => span.resourceId))].flatMap((id) => {
		const resource = resourceById.get(id);
		return resource === undefined ? [] : [resource];
	});
	const { start, end } = traceBounds(trace);
	const from = start - minutes * 60_000;
	const to = end + minutes * 60_000;
	const { events, query, error, isLoading } = useWorkspaceEvents(
		workspaceId,
		new Map(resources.map((resource) => [resource.id, resource.slug])),
		{
			types: ENVIRONMENT_EVENT_TYPES,
			resourceIds: resources.map((resource) => resource.id),
			startMs: from,
			endMs: to,
		},
		!installation,
	);
	const items = events.map(eventItem);
	return {
		items,
		isLoading,
		error,
		more: query.hasNextPage,
		older: query,
		from,
		to,
		names: resources.map((resource) => resource.slug),
	};
}

export function TraceEvents({
	trace,
	events,
	minutes,
	onMinutes,
}: {
	trace: TraceSummary;
	events: ReturnType<typeof useTraceEvents>;
	minutes: number;
	onMinutes: (minutes: number) => void;
}) {
	const timePref = useTimePreferences();
	const { goTo } = useObs();
	const { color } = useTraceResources();
	const { start, end } = traceBounds(trace);
	const window = WINDOWS.find((w) => w.value === minutes)?.label ?? "";
	const when = (ts: number) =>
		ts < start
			? `${shortDurationLabel(start - ts)} before`
			: ts > end
				? `${shortDurationLabel(ts - end)} after`
				: "During this trace";
	return (
		<section aria-label="Trace events" className="flex min-w-0 flex-col gap-1 px-5 pt-3 pb-4">
			<div className="mb-2">
				<WindowToggle label="Events window" minutes={minutes} onMinutes={onMinutes} />
			</div>
			{events.isLoading ? (
				<p role="status" className="m-0 flex items-center gap-2 py-3 text-fg3">
					<Spinner />
					Loading events…
				</p>
			) : events.error !== null ? (
				<p role="alert" className="m-0 py-3 text-bad-fg">
					{getErrorMessage(events.error, "Couldn’t load events")}
				</p>
			) : events.items.length === 0 ? (
				<p className="m-0 py-3.5 text-[12px] text-fg3">
					{events.names.length === 0
						? "No span in this trace belongs to a Loco resource."
						: `Nothing happened to ${events.names.join(", ")} within ${window} of this trace.`}
				</p>
			) : (
				<ol className="m-0 list-none p-0">
					{events.items.map((event) => {
						const span = trace.spans.find((s) => s.resourceId === event.resourceId);
						return (
							<li key={event.id}>
								<button
									type="button"
									aria-label={`Open ${event.title} in Events`}
									onClick={() => {
										goTo("events", {
											range: { kind: "absolute", from: events.from, to: events.to },
											tokens: [{ key: "resource", value: event.resourceName, neg: false }],
										});
									}}
									className="-mx-3 grid w-[calc(100%+24px)] grid-cols-[18px_minmax(0,1fr)_auto] items-center gap-3 rounded-md px-3 py-2.5 text-left hover:bg-panel focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none sm:grid-cols-[18px_minmax(0,1fr)_120px_150px]"
								>
									<span className="flex">
										<EventIcon item={event} />
									</span>
									<span className="flex min-w-0 flex-col gap-0.5">
										<span className="text-[13px]">{event.title}</span>
										{event.detail !== "" && (
											<span className="font-mono text-[11.5px] text-fg3 [overflow-wrap:anywhere]">{event.detail}</span>
										)}
									</span>
									<span className="col-start-2 row-start-2 flex min-w-0 items-center gap-1.5 text-[12.5px] text-fg2 sm:col-start-3 sm:row-start-1">
										<i
											className="size-2 shrink-0 rounded-full"
											style={{ background: span === undefined ? "var(--fg3)" : color(span) }}
										/>
										<span className="truncate">{event.resourceName}</span>
									</span>
									<span className="col-start-3 row-span-2 row-start-1 flex flex-col items-end gap-0.5 text-[11.5px] text-fg3 sm:col-start-4 sm:row-span-1">
										<span className="font-mono text-[12px] text-fg2">{formatClock(event.ts, false, timePref)}</span>
										<span>{when(event.ts)}</span>
									</span>
								</button>
							</li>
						);
					})}
				</ol>
			)}
			{events.older.isFetchNextPageError && (
				<p role="alert" className="m-0 py-2 text-bad-fg">
					{getErrorMessage(events.older.error, "Couldn’t load older events")}
				</p>
			)}
			{events.more && (
				<div className="pt-1" aria-busy={events.older.isFetchingNextPage}>
					<Button
						variant="plain-link"
						disabled={events.older.isFetching}
						onClick={() => {
							void events.older.fetchNextPage();
						}}
					>
						{events.older.isFetchingNextPage ? "Loading…" : "Older events"}
					</Button>
				</div>
			)}
		</section>
	);
}
