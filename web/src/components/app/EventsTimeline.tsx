import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useStreamEvents } from "@/hooks/useStreamEvents";
import { AlertCircle, CheckCircle2 } from "lucide-react";
import { getErrorMessage } from "@/lib/error-handler";

interface EventsTimelineProps {
	resourceId: string;
	isLoading?: boolean;
}

export function EventsTimeline({ resourceId, isLoading = false }: EventsTimelineProps) {
	const { events, error } = useStreamEvents(resourceId);

	if (isLoading) {
		return (
			<Card className="animate-pulse">
				<CardContent className="p-6">
					<div className="h-6 bg-main/20 rounded w-1/4"></div>
				</CardContent>
			</Card>
		);
	}

	if (error) {
		return (
			<Card className="border-2 border-destructive/50">
				<CardHeader>
					<CardTitle>Events</CardTitle>
				</CardHeader>
				<CardContent>
					<div className="flex items-center gap-2 text-sm text-destructive">
						<AlertCircle className="w-5 h-5 shrink-0" />
						<p>{getErrorMessage(error, "Failed to load events")}</p>
					</div>
				</CardContent>
			</Card>
		);
	}

	if (events.length === 0) {
		return null;
	}

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle>Events</CardTitle>
			</CardHeader>
			<CardContent>
				<div className="space-y-4">
					{events.map((event, index) => (
						<div key={index} className="flex gap-4">
							{/* Icon */}
							<div className="shrink-0 mt-1">
								{event.severity === "Warning" ? (
									<AlertCircle className="w-5 h-5 text-yellow-600" />
								) : (
									<CheckCircle2 className="w-5 h-5 text-green-600" />
								)}
							</div>

							{/* Content */}
							<div className="flex-1 min-w-0">
								<div className="flex items-center justify-between gap-2 mb-1">
									<p className="text-sm font-medium text-foreground">
										{event.eventType}
									</p>
									<Badge
										variant="secondary"
										className="text-xs"
									>
										{new Date(event.timestamp).toLocaleTimeString()}
									</Badge>
								</div>
								<p className="text-sm text-foreground opacity-70">
									{event.message}
								</p>
								{event.pod && (
									<p className="text-xs text-foreground opacity-50 mt-1 font-mono">
										Pod: {event.pod}
									</p>
								)}
							</div>
						</div>
					))}
				</div>
			</CardContent>
		</Card>
	);
}
