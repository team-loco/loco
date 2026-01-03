import { Button } from "@/components/ui/button";
import {
	getRecentEvents,
	subscribeToEvents,
	type WorkspaceEvent,
} from "@/lib/events";
import { X } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

export function EventsList() {
	const [events, setEvents] = useState<WorkspaceEvent[]>(() => getRecentEvents(5));

	useEffect(() => {
		// Subscribe to new events
		const unsubscribe = subscribeToEvents("workspace", (event) => {
			setEvents((prev) => [event, ...prev.slice(0, 4)]);
			if (event.severity === "error") {
				toast.error(event.message, {
					description: `${event.resourceName} • ${formatTime(event.timestamp)}`,
				});
			} else if (event.severity === "warning") {
				toast.warning(event.message, {
					description: `${event.resourceName} • ${formatTime(event.timestamp)}`,
				});
			} else {
				toast.success(event.message, {
					description: `${event.resourceName} • ${formatTime(event.timestamp)}`,
				});
			}
		});

		return unsubscribe;
	}, []);

	const handleDismiss = (eventId: string) => {
		setEvents((prev) => prev.filter((e) => e.id !== eventId));
	};

	if (events.length === 0) {
		return (
			<div className="px-2 py-4 text-center text-xs text-foreground opacity-60">
				No events yet
			</div>
		);
	}

	return (
		<div className="space-y-2">
			{events.map((event) => (
				<div
					key={event.id}
					className="text-xs border border-border rounded p-2 flex gap-2 items-start"
				>
					<div className="flex-1 min-w-0">
						<p className="font-medium text-foreground">{event.resourceName}</p>
						<p className="text-foreground opacity-70 wrap-break-word">
							{event.message}
						</p>
						<p className="text-foreground opacity-50 mt-0.5">
							{formatTime(event.timestamp)}
						</p>
					</div>
					<Button
						variant="secondary"
						size="sm"
						onClick={() => handleDismiss(event.id)}
						className="h-4 w-4 p-0 shrink-0"
					>
						<X className="w-3 h-3" />
					</Button>
				</div>
			))}
		</div>
	);
}

function formatTime(date: Date): string {
	const now = new Date();
	const diff = now.getTime() - date.getTime();
	const seconds = Math.floor(diff / 1000);
	const minutes = Math.floor(seconds / 60);
	const hours = Math.floor(minutes / 60);

	if (seconds < 60) return "just now";
	if (minutes === 1) return "1m ago";
	if (minutes < 60) return `${minutes}m ago`;
	if (hours === 1) return "1h ago";
	if (hours < 24) return `${hours}h ago`;
	return date.toLocaleDateString();
}
