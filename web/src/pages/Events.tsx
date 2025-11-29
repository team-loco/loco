import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import {
	getRecentEvents,
	subscribeToEvents,
	type WorkspaceEvent,
} from "@/lib/events";
import { AlertCircle, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

const severityColors: Record<string, string> = {
	error: "border-l-4 border-l-red-500 bg-red-50 dark:bg-red-950",
	warning: "border-l-4 border-l-yellow-500 bg-yellow-50 dark:bg-yellow-950",
	success: "border-l-4 border-l-green-500 bg-green-50 dark:bg-green-950",
	info: "border-l-4 border-l-blue-500 bg-blue-50 dark:bg-blue-950",
};

const severityBadgeColors: Record<string, string> = {
	error: "bg-red-200 text-red-900 dark:bg-red-900 dark:text-red-100",
	warning: "bg-yellow-200 text-yellow-900 dark:bg-yellow-900 dark:text-yellow-100",
	success: "bg-green-200 text-green-900 dark:bg-green-900 dark:text-green-100",
	info: "bg-blue-200 text-blue-900 dark:bg-blue-900 dark:text-blue-100",
};

export function Events() {
	const initialEvents = getRecentEvents(100);
	const [events, setEvents] = useState<WorkspaceEvent[]>(() => initialEvents);
	const [filteredEvents, setFilteredEvents] = useState<WorkspaceEvent[]>(() => initialEvents);
	const [searchTerm, setSearchTerm] = useState("");
	const [severityFilter, setSeverityFilter] = useState<string>("all");

	useEffect(() => {
		// Subscribe to new events
		const unsubscribe = subscribeToEvents("workspace", (event) => {
			setEvents((prev) => [event, ...prev]);
		});

		return unsubscribe;
	}, []);

	// useMemo ensures filtering is only recalculated when dependencies change
	const filtered = useMemo(() => {
		let result = events;

		if (searchTerm) {
			const term = searchTerm.toLowerCase();
			result = result.filter(
				(e) =>
					e.appName.toLowerCase().includes(term) ||
					e.message.toLowerCase().includes(term)
			);
		}

		if (severityFilter !== "all") {
			result = result.filter((e) => e.severity === severityFilter);
		}

		return result;
	}, [events, searchTerm, severityFilter]);

	useEffect(() => {
		setFilteredEvents(filtered);
	}, [filtered]);

	const handleDismiss = (eventId: string) => {
		setEvents((prev) => prev.filter((e) => e.id !== eventId));
	};

	const handleClearAll = () => {
		setEvents([]);
	};

	return (
		<div className="min-h-screen bg-background">
			{/* Header */}
			<div className="border-b-2 border-border bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/60 sticky top-0 z-40">
				<div className="container px-4 py-4 flex items-center justify-between">
					<div>
						<h1 className="text-2xl font-bold">Events</h1>
						<p className="text-sm text-foreground opacity-70 mt-1">
							View all workspace events and activity
						</p>
					</div>
					{events.length > 0 && (
						<Button
							variant="destructive"
							size="sm"
							onClick={handleClearAll}
							className="flex items-center gap-2"
						>
							<Trash2 className="h-4 w-4" />
							Clear All
						</Button>
					)}
				</div>
			</div>

			{/* Filters */}
			<div className="container px-4 py-6 space-y-4">
				<div className="flex flex-col sm:flex-row gap-4">
					<div className="flex-1">
						<Input
							placeholder="Search events by app or message..."
							value={searchTerm}
							onChange={(e) => setSearchTerm(e.target.value)}
							className="border-2 border-border rounded-neo"
						/>
					</div>
					<Select value={severityFilter} onValueChange={setSeverityFilter}>
						<SelectTrigger className="w-full sm:w-40 border-2 border-border rounded-neo">
							<SelectValue placeholder="All severities" />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="all">All severities</SelectItem>
							<SelectItem value="error">Error</SelectItem>
							<SelectItem value="warning">Warning</SelectItem>
							<SelectItem value="success">Success</SelectItem>
							<SelectItem value="info">Info</SelectItem>
						</SelectContent>
					</Select>
				</div>

				{/* Event count */}
				<div className="text-xs text-foreground opacity-70">
					Showing {filteredEvents.length} of {events.length} events
				</div>
			</div>

			{/* Events List */}
			<div className="container px-4 pb-8">
				{filteredEvents.length === 0 ? (
					<div className="py-12 text-center">
						<AlertCircle className="h-12 w-12 mx-auto text-foreground opacity-30 mb-3" />
						<p className="text-foreground opacity-60">
							{events.length === 0
								? "No events yet"
								: "No events match your filters"}
						</p>
					</div>
				) : (
					<div className="space-y-3">
						{filteredEvents.map((event) => (
							<div
								key={event.id}
								className={`border-2 border-border rounded-neo p-4 ${
									severityColors[event.severity] || ""
								}`}
							>
								<div className="flex items-start justify-between gap-4">
									<div className="flex-1 min-w-0">
										<div className="flex items-center gap-3 mb-2">
											<h3 className="font-semibold text-foreground">
												{event.appName}
											</h3>
											<span
												className={`text-xs px-2 py-1 rounded-full font-medium ${
													severityBadgeColors[event.severity] || ""
												}`}
											>
												{event.severity.charAt(0).toUpperCase() +
													event.severity.slice(1)}
											</span>
										</div>
										<p className="text-sm text-foreground opacity-80 wrap-break-word mb-2">
															{event.message}
														</p>
										<p className="text-xs text-foreground opacity-60">
											{formatDateTime(event.timestamp)}
										</p>
									</div>
									<Button
										variant="neutral"
										size="sm"
										onClick={() => handleDismiss(event.id)}
										className="h-8 w-8 p-0 shrink-0"
										title="Dismiss event"
									>
										<X className="h-4 w-4" />
									</Button>
								</div>
							</div>
						))}
					</div>
				)}
			</div>
		</div>
	);
}

function formatDateTime(date: Date): string {
	const now = new Date();
	const diff = now.getTime() - date.getTime();
	const seconds = Math.floor(diff / 1000);
	const minutes = Math.floor(seconds / 60);
	const hours = Math.floor(minutes / 60);
	const days = Math.floor(hours / 24);

	if (seconds < 60) return "just now";
	if (minutes === 1) return "1 minute ago";
	if (minutes < 60) return `${minutes} minutes ago`;
	if (hours === 1) return "1 hour ago";
	if (hours < 24) return `${hours} hours ago`;
	if (days === 1) return "Yesterday";
	if (days < 7) return `${days} days ago`;

	return date.toLocaleDateString("en-US", {
		weekday: "short",
		month: "short",
		day: "numeric",
		hour: "2-digit",
		minute: "2-digit",
	});
}
