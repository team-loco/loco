import { useTimePreferences } from "@/lib/use-time-preferences";
import { Link } from "react-router";
import type { RuntimeEvent as Event } from "@/lib/runtime-events";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { agoLabel, maybeTsMs } from "@/lib/time";

import { eventRows } from "./events";
import { formatFullTime } from "./format";

export type EventsTab = "all" | "warnings";

const TABS: { value: EventsTab; label: string }[] = [
	{ value: "all", label: "All" },
	{ value: "warnings", label: "Warnings" },
];

function EventList({
	events,
	warningsOnly,
	isLoading,
	error,
	nowMs,
}: {
	events: Event[];
	warningsOnly: boolean;
	isLoading: boolean;
	error: unknown;
	nowMs: number;
}) {
	const timePref = useTimePreferences();
	if (isLoading) {
		return (
			<div className="flex flex-col gap-2">
				<Skeleton className="h-10 w-full" />
				<Skeleton className="h-10 w-full" />
				<Skeleton className="h-10 w-full" />
			</div>
		);
	}
	if (error !== null)
		return <p className="py-10 text-center text-bad-fg">{getErrorMessage(error, "Failed to load events")}</p>;
	const shown = eventRows(events).filter((row) => !warningsOnly || row.warning);
	if (shown.length === 0) {
		return <p className="py-10 text-center text-fg3">{warningsOnly ? "No warnings." : "No events yet."}</p>;
	}
	return (
		<ol className="m-0 list-none p-0">
			{shown.map((row) => {
				const ms = maybeTsMs(row.event.timestamp);
				return (
					<li
						key={row.event.id}
						className="grid grid-cols-[16px_minmax(0,1fr)_auto] items-start gap-[11px] border-b border-line2 py-2.5 last:border-b-0"
					>
						<span className="pt-0.5">
							<StatusGlyph glyph={row.glyph} />
							<span className="sr-only">{row.warning ? "Warning" : "Normal"}</span>
						</span>
						<span className="flex min-w-0 flex-col gap-0.5">
							<span className="flex flex-wrap items-center gap-[7px] text-[13.5px]">
								{row.title}
								{row.count > 1 && <Badge tone="pill">{`×${row.count.toString()}`}</Badge>}
							</span>
							{row.detail !== "" && (
								<span className="font-mono text-[11.5px] [overflow-wrap:anywhere] text-fg3">{row.detail}</span>
							)}
						</span>
						<span
							className="font-mono text-[11.5px] whitespace-nowrap text-fg3"
							title={ms === undefined ? undefined : formatFullTime(ms, false, timePref)}
						>
							{ms === undefined ? "—" : agoLabel(ms, nowMs)}
						</span>
					</li>
				);
			})}
		</ol>
	);
}

export function EventsDrawer({
	open,
	onOpenChange,
	tab,
	onTab,
	resourceName,
	events,
	isLoading,
	error,
	eventsHref,
	nowMs,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	tab: EventsTab;
	onTab: (tab: EventsTab) => void;
	resourceName: string;
	events: Event[];
	isLoading: boolean;
	error: unknown;
	eventsHref: string | undefined;
	nowMs: number;
}) {
	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent size="wide">
				<SheetHeader className="flex-wrap">
					<div className="flex min-w-0 flex-1 items-baseline gap-2.5">
						<SheetTitle>Events</SheetTitle>
						<span className="truncate font-mono text-[12.5px] text-fg3">{resourceName}</span>
					</div>
					<ToggleGroup
						variant="segmented"
						aria-label="Show"
						value={[tab]}
						onValueChange={(v: string[]) => {
							const next = TABS.find((t) => t.value === v[0]);
							if (next !== undefined) onTab(next.value);
						}}
					>
						{TABS.map((t) => (
							<ToggleGroupItem key={t.value} value={t.value}>
								{t.label}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
				</SheetHeader>
				<SheetBody className="py-2">
					<EventList
						nowMs={nowMs}
						events={events}
						warningsOnly={tab === "warnings"}
						isLoading={isLoading}
						error={error}
					/>
				</SheetBody>
				{eventsHref !== undefined && (
					<SheetFooter className="justify-between">
						<span className="text-[12.5px] text-fg3">Last 7 days</span>
						<Button variant="link" nativeButton={false} render={<Link to={eventsHref} />}>
							Open in Events
						</Button>
					</SheetFooter>
				)}
			</SheetContent>
		</Sheet>
	);
}
