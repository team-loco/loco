import { useMemo } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Card, CardContent } from "@/components/ui/card";
import { Loader2 } from "lucide-react";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { listWorkspaceResources } from "@/gen/loco/resource/v1";
import { useQuery } from "@connectrpc/connect-query";
import { ObsProvider } from "@/components/observability/ObsProvider";
import { ObsToolbar } from "@/components/observability/ObsToolbar";
import { ObsOverview } from "@/components/observability/ObsOverview";
import { ObsLogs } from "@/components/observability/ObsLogs";
import { ObsMetrics } from "@/components/observability/ObsMetrics";

function ObsContent() {
	return (
		<div className="space-y-4">
			<ObsToolbar />
			<Tabs defaultValue="overview">
				<TabsList>
					<TabsTrigger value="overview">Overview</TabsTrigger>
					<TabsTrigger value="logs">Logs</TabsTrigger>
					<TabsTrigger value="metrics">Metrics</TabsTrigger>
				</TabsList>
				<TabsContent value="overview" className="mt-4">
					<ObsOverview />
				</TabsContent>
				<TabsContent value="logs" className="mt-4">
					<ObsLogs />
				</TabsContent>
				<TabsContent value="metrics" className="mt-4">
					<ObsMetrics />
				</TabsContent>
			</Tabs>
		</div>
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
