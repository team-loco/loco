import { AppHeader } from "@/components/app/AppHeader";
import { DeploymentStatusCard } from "@/components/app/DeploymentStatusCard";
import { EnvironmentVariables } from "@/components/app/EnvironmentVariables";
import { EventsTimeline } from "@/components/app/EventsTimeline";
import { LogsViewer } from "@/components/app/LogsViewer";
import { RecentDeployments } from "@/components/app/RecentDeployments";
import { Card, CardContent } from "@/components/ui/card";
import { useResourceDetails } from "@/hooks/useResourceDetails";
import { subscribeToEvents } from "@/lib/events";
import { useEffect } from "react";
import { useParams } from "react-router";
import Loader from "@/assets/loader.svg?react";

export function ResourceDetails() {
	const { resourceId } = useParams<{ resourceId: string }>();
	const { resource, deployments, isLoading, error } = useResourceDetails(resourceId ?? "");

	// Subscribe to real-time resource-specific events
	useEffect(() => {
		if (!resourceId) return;

		const unsubscribe = subscribeToEvents(`resource:${resourceId}`, (event) => {
			// Event subscription triggers refetches via the hook's internal logic
			console.log(`[Resource ${resourceId}] Event: ${event.type}`);
		});

		return unsubscribe;
	}, [resourceId]);

	if (!resourceId) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-2">Invalid Resource ID</p>
						<p className="text-sm text-foreground opacity-70">
							The resource ID is missing from the URL
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center">
					<div className="inline-flex gap-2 items-center flex-col">
						<Loader className="w-8 h-8" />
						<p className="text-foreground font-base">Loading resource...</p>
					</div>
				</div>
			</div>
		);
	}

	if (error) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-4">
							Error Loading Resource
						</p>
						<p className="text-sm text-foreground opacity-70 mb-4">
							{error instanceof Error ? error.message : "Unknown error"}
						</p>
						<p className="text-xs text-foreground opacity-50">
							Make sure the resource exists and you have access to it
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (!resource) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-2">Resource Not Found</p>
						<p className="text-sm text-foreground opacity-70">
							The resource with ID {resourceId} does not exist
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	return (
		<div className="space-y-6">
			{/* Resource Header */}
			<AppHeader resource={resource} isLoading={isLoading} />

			{/* Active Deployment Card */}
			<DeploymentStatusCard
				resourceId={resourceId}
				deployment={deployments[0]}
				isLoading={isLoading}
			/>

			{/* Previous Deployments */}
			<RecentDeployments
				deployments={deployments.slice(1)}
				resourceId={resourceId}
				isLoading={isLoading}
			/>

			{/* Environment Variables */}
			<EnvironmentVariables resourceId={resourceId} envVars={[]} isLoading={isLoading} />

			{/* Logs Viewer */}
			<LogsViewer resourceId={resourceId} isLoading={isLoading} />

			{/* Events Timeline */}
			<EventsTimeline resourceId={resourceId} isLoading={isLoading} />
		</div>
	);
}
