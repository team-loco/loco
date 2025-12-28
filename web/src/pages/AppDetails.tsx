import { AppHeader } from "@/components/app/AppHeader";
import { DeploymentStatusCard } from "@/components/app/DeploymentStatusCard";
import { EnvironmentVariables } from "@/components/app/EnvironmentVariables";
import { EventsTimeline } from "@/components/app/EventsTimeline";
import { LogsViewer } from "@/components/app/LogsViewer";
import { RecentDeployments } from "@/components/app/RecentDeployments";
import { ScaleCard } from "@/components/app/ScaleCard";
import { Card, CardContent } from "@/components/ui/card";
import { useAppDetails } from "@/hooks/useAppDetails";
import { subscribeToEvents } from "@/lib/events";
import { useEffect } from "react";
import { useParams } from "react-router";

export function AppDetails() {
	const { appId } = useParams<{ appId: string }>();
	const { app, status, deployments, isLoading, error } = useAppDetails(appId ?? "");

	// Subscribe to real-time app-specific events
	useEffect(() => {
		if (!appId) return;

		const unsubscribe = subscribeToEvents(`app:${appId}`, (event) => {
			// Event subscription triggers refetches via the hook's internal logic
			console.log(`[App ${appId}] Event: ${event.type}`);
		});

		return unsubscribe;
	}, [appId]);

	if (!appId) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-2">Invalid App ID</p>
						<p className="text-sm text-foreground opacity-70">
							The app ID is missing from the URL
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
					<div className="inline-flex gap-2 items-center">
						<div className="w-4 h-4 bg-main rounded-full animate-pulse"></div>
						<p className="text-foreground font-base">Loading app...</p>
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
							Error Loading App
						</p>
						<p className="text-sm text-foreground opacity-70 mb-4">
							{error instanceof Error ? error.message : "Unknown error"}
						</p>
						<p className="text-xs text-foreground opacity-50">
							Make sure the app exists and you have access to it
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (!app) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-2">App Not Found</p>
						<p className="text-sm text-foreground opacity-70">
							The app with ID {appId} does not exist
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	return (
		<div className="space-y-6">
			{/* App Header */}
			<AppHeader app={app} isLoading={isLoading} />

			{/* Deployment Status Card */}
			<DeploymentStatusCard
				appId={appId}
				deployment={deployments[0]}
				isLoading={isLoading}
			/>

			{/* Recent Deployments */}
			<RecentDeployments
				deployments={deployments}
				appId={appId}
				isLoading={isLoading}
			/>

			{/* Scale Card */}
			<ScaleCard appId={appId} currentReplicas={status?.replicas} isLoading={isLoading} />

			{/* Environment Variables */}
			<EnvironmentVariables
				appId={appId}
				envVars={[]}
				isLoading={isLoading}
			/>

			{/* Logs Viewer */}
			<LogsViewer appId={appId} isLoading={isLoading} />

			{/* Events Timeline */}
			<EventsTimeline appId={appId} isLoading={isLoading} />
		</div>
	);
}
