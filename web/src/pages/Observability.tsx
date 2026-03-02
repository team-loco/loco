import { ObsLogs } from "@/components/observability/ObsLogs";
import { ObsMetrics } from "@/components/observability/ObsMetrics";
import { ObsOverview } from "@/components/observability/ObsOverview";
import { ObsProvider } from "@/components/observability/ObsProvider";
import { ObsToolbar } from "@/components/observability/ObsToolbar";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { listWorkspaceResources } from "@/gen/loco/resource/v1";
import { useQuery } from "@connectrpc/connect-query";
import { AudioWaveform, Loader2, LogsIcon, Waypoints } from "lucide-react";
import { useMemo } from "react";

function ObsContent() {
	return (
		<Card className="flex flex-col w-[95%] mx-auto">
			<CardContent className="mx-auto flex flex-col h-full w-full">
				<Tabs defaultValue="logs" className="flex flex-col h-full">
					<div className="flex items-center justify-between gap-4 flex-wrap pb-4">
						<ObsToolbar />
						<TabsList>
							<TabsTrigger value="logs" className="flex items-center gap-2">
								<LogsIcon className="h-3.5 w-3.5" />
								Logs
							</TabsTrigger>
							<TabsTrigger value="metrics" className="flex items-center gap-2">
								<AudioWaveform className="h-3.5 w-3.5" />
								Metrics
							</TabsTrigger>
							<TabsTrigger value="overview" className="flex items-center gap-2">
								<Waypoints className="h-3.5 w-3.5" />
								Traces
							</TabsTrigger>
						</TabsList>
					</div>
					<TabsContent value="logs" className="mt-0">
						<ObsLogs />
					</TabsContent>
					<TabsContent value="metrics" className="mt-0">
						<ObsMetrics />
					</TabsContent>
					<TabsContent value="overview" className="mt-0">
						<ObsOverview />
					</TabsContent>
				</Tabs>
			</CardContent>
		</Card>
	);
}

export function Observability() {
	const { activeWorkspaceId } = useOrgWorkspace();

	const { data: resourcesRes, isLoading: resourcesLoading } = useQuery(
		listWorkspaceResources,
		activeWorkspaceId ? { workspaceId: activeWorkspaceId } : undefined,
		{ enabled: !!activeWorkspaceId },
	);

	const resources = useMemo(
		() => resourcesRes?.resources ?? [],
		[resourcesRes],
	);

	if (!activeWorkspaceId) {
		return (
			<Card>
				<CardContent className="flex items-center justify-center py-12 text-muted-foreground text-sm">
					Select a workspace to view observability data.
				</CardContent>
			</Card>
		);
	}

	if (resourcesLoading) {
		return (
			<div className="flex items-center justify-center py-16 gap-2 text-muted-foreground">
				<Loader2 className="h-4 w-4 animate-spin" />
				Loading…
			</div>
		);
	}

	return (
		<ObsProvider workspaceId={activeWorkspaceId} resources={resources}>
			<ObsContent />
		</ObsProvider>
	);
}
