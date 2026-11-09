import { Link } from "react-router";
import type { Event } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { agoLabel, maybeTsMs } from "@/lib/time";
import { ActivityPanel } from "@/pages/settings/ActivityPanel";

import { isWarning } from "./events";
import { formatFullTime } from "./format";

export type EventsTab = "all" | "warnings" | "activity";

const TABS: { value: EventsTab; label: string }[] = [
	{ value: "all", label: "All" },
	{ value: "warnings", label: "Warnings" },
	{ value: "activity", label: "Activity" },
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
	if (isLoading) {
		return (
			<div className="flex flex-col gap-2">
				<Skeleton className="h-10 w-full" />
				<Skeleton className="h-10 w-full" />
				<Skeleton className="h-10 w-full" />
			</div>
		);
	}
	if (isUnimplemented(error)) {
		return (
			<p className="flex items-center justify-center gap-2 py-10 text-fg3">
				Kubernetes events for this resource are not recorded yet
				<SoonTag />
			</p>
		);
	}
	if (error !== null)
		return <p className="py-10 text-center text-bad-fg">{getErrorMessage(error, "Failed to load events")}</p>;
	const shown = warningsOnly ? events.filter(isWarning) : events;
	if (shown.length === 0) {
		return <p className="py-10 text-center text-fg3">{warningsOnly ? "No warnings." : "No events yet."}</p>;
	}
	return (
		<ol className="m-0 list-none p-0">
			{shown.map((e, i) => {
				const ms = maybeTsMs(e.timestamp);
				return (
					<li
						key={`${String(ms)}-${i.toString()}`}
						className="grid grid-cols-[16px_minmax(0,1fr)_auto] items-start gap-[11px] border-b border-line2 py-2.5 last:border-b-0"
					>
						<span className="pt-0.5">
							<StatusGlyph glyph={isWarning(e) ? "warn" : "off"} />
							<span className="sr-only">{isWarning(e) ? "Warning" : "Normal"}</span>
						</span>
						<span className="flex min-w-0 flex-col gap-0.5">
							<span className="text-[13.5px]">{e.reason}</span>
							<span className="text-[12.5px] break-words text-fg2">{e.message}</span>
							{e.podName !== "" && <span className="truncate font-mono text-[11.5px] text-fg3">{e.podName}</span>}
						</span>
						<span
							className="font-mono text-[11.5px] whitespace-nowrap text-fg3"
							title={ms === undefined ? undefined : formatFullTime(ms)}
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
	resourceId,
	resourceName,
	orgId,
	workspaceId,
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
	resourceId: string;
	resourceName: string;
	orgId: string;
	workspaceId: string;
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
					<div className="flex min-w-0 flex-1 flex-col">
						<SheetTitle>Events</SheetTitle>
						<span className="truncate font-mono text-[12px] text-fg3">{resourceName}</span>
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
					{tab === "activity" ? (
						<ActivityPanel
							key={resourceId}
							orgId={orgId}
							workspaceId={workspaceId}
							resourceId={resourceId}
							title="Activity"
							description={`Changes made to ${resourceName}, newest first.`}
							names={new Map([[resourceId, resourceName]])}
						/>
					) : (
						<EventList
							nowMs={nowMs}
							events={events}
							warningsOnly={tab === "warnings"}
							isLoading={isLoading}
							error={error}
						/>
					)}
				</SheetBody>
				{eventsHref !== undefined && (
					<SheetFooter className="justify-between">
						<span className="text-[12.5px] text-fg3">Newest first</span>
						<Button variant="link" nativeButton={false} render={<Link to={eventsHref} />}>
							Open in Events
						</Button>
					</SheetFooter>
				)}
			</SheetContent>
		</Sheet>
	);
}
