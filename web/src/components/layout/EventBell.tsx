import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	getRecentEvents,
	subscribeToEvents,
	type WorkspaceEvent,
} from "@/lib/events";
import { Bell, X } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

export function EventBell() {
	const [events, setEvents] = useState<WorkspaceEvent[]>(() => getRecentEvents(10));
	const [unreadCount, setUnreadCount] = useState(0);

	useEffect(() => {
		// Subscribe to new events
		const unsubscribe = subscribeToEvents("workspace", (event) => {
			setEvents((prev) => [event, ...prev.slice(0, 9)]);
			setUnreadCount((prev) => prev + 1);
			if (event.severity === "error") {
				toast.error(event.message, {
					description: `${event.appName} • ${formatTime(event.timestamp)}`,
				});
			} else if (event.severity === "warning") {
				toast.warning(event.message, {
					description: `${event.appName} • ${formatTime(event.timestamp)}`,
				});
			} else {
				toast.success(event.message, {
					description: `${event.appName} • ${formatTime(event.timestamp)}`,
				});
			}
		});

		return unsubscribe;
	}, []);

	const handleClearAll = () => {
		setEvents([]);
		setUnreadCount(0);
	};

	const handleDismiss = (eventId: string) => {
		setEvents((prev) => prev.filter((e) => e.id !== eventId));
	};

	const handleOpen = () => {
		setUnreadCount(0);
	};

	return (
		<DropdownMenu onOpenChange={(open) => open && handleOpen()}>
			<DropdownMenuTrigger asChild>
				<span className="flex">
					<Bell className="w-4 h-4" /> Recent Events
					{unreadCount > 0 && (
						<span className="absolute -top-2 -right-2 bg-destructive text-white text-xs font-bold rounded-full w-5 h-5 flex items-center justify-center">
							{unreadCount > 9 ? "9+" : unreadCount}
						</span>
					)}
				</span>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-80">
				{events.length === 0 ? (
					<div className="px-4 py-6 text-center text-sm text-foreground opacity-60">
						No events yet
					</div>
				) : (
					<div className="max-h-96 overflow-y-auto">
						{/* Header with Clear All */}
						<div className="sticky top-0 bg-background border-b border-border p-3 flex items-center justify-between">
							<p className="text-xs font-medium text-foreground uppercase opacity-60">
								Recent Events ({events.length})
							</p>
							{events.length > 0 && (
								<Button
									variant="secondary"
									size="sm"
									onClick={handleClearAll}
									className="h-6 text-xs"
								>
									Clear All
								</Button>
							)}
						</div>

						{/* Events List */}
						<div className="divide-y divide-border">
							{events.map((event) => (
								<div
									key={event.id}
									className="p-3 hover:bg-background/50 transition-colors"
								>
									<div className="flex items-start justify-between gap-2">
										<div className="flex-1 min-w-0">
											<p className="text-sm font-medium text-foreground">
												{event.appName}
											</p>
											<p className="text-xs text-foreground opacity-70 mt-0.5 wrap-break-word">
												{event.message}
											</p>
											<p className="text-xs text-foreground opacity-50 mt-1">
												{formatTime(event.timestamp)}
											</p>
										</div>
										<Button
											variant="secondary"
											size="sm"
											onClick={() => handleDismiss(event.id)}
											className="h-5 w-5 p-0 shrink-0"
										>
											<X className="w-3 h-3" />
										</Button>
									</div>
								</div>
							))}
						</div>
					</div>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
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
