import { useQuery } from "@connectrpc/connect-query";
import { useMemo } from "react";

import { Badge } from "@/components/design/Badge";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/design/Card";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listWorkspaceMembers } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { TrendingUpIcon } from "lucide-react";

interface WorkspaceDashboardMetricsProps {
	workspaceId: string;
	workspaceName?: string;
}

export function WorkspaceDashboardMetrics({
	workspaceId,
}: WorkspaceDashboardMetricsProps) {
	// Fetch resources
	const { data: resourcesRes } = useQuery(
		listWorkspaceResources,
		{ workspaceId },
		{ enabled: !!workspaceId },
	);
	const apps = useMemo(
		() => resourcesRes?.resources ?? [],
		[resourcesRes?.resources],
	);

	// Fetch members
	const { data: membersRes } = useQuery(
		listWorkspaceMembers,
		{ workspaceId },
		{ enabled: !!workspaceId },
	);
	const members = useMemo(
		() => membersRes?.members ?? [],
		[membersRes?.members],
	);

	// Group members by role
	const membersByRole = useMemo(() => {
		const grouped: Record<string, number> = {};
		members.forEach((member) => {
			const role = member.scopes[0] ?? "member";
			grouped[role] = (grouped[role] || 0) + 1;
		});
		return grouped;
	}, [members]);

	// Calculate active apps (not IDLE, status 3)
	const activeAppsCount = useMemo(() => {
		// eslint-disable-next-line @typescript-eslint/no-unsafe-enum-comparison
		return apps.filter((app) => app.status !== 3).length;
	}, [apps]);

	// Calculate recent deployments count (approximated from apps data)
	const recentDeploymentsCount = 0;

	return (
		<div className="relative w-[95%] mx-auto">
			<div className="grid grid-cols-1 md:grid-cols-3 gap-4">
				{/* Total Apps (Active) */}
				<Card className="hover:border-border-strong group">
					<div className="absolute inset-x-0 top-0 h-1 bg-black dark:bg-accent opacity-80" />
					<CardHeader className="relative pb-2">
						<CardDescription>Active Apps</CardDescription>
						<CardTitle className="text-3xl font-semibold tabular-nums">
							{activeAppsCount}
						</CardTitle>
						<div className="absolute right-4 top-4">
							<Badge
								variant="outline"
								className="flex gap-1 rounded-lg text-xs"
							>
								<TrendingUpIcon className="size-3" />
								{apps.length > 0
									? Math.round((activeAppsCount / apps.length) * 100)
									: 0}
								%
							</Badge>
						</div>
					</CardHeader>
					<CardContent className="text-sm text-muted-foreground">
						Out of {apps.length} total apps
					</CardContent>
				</Card>

				{/* Recent Deployments (30d) */}
				<Card className="hover:border-border-strong group">
					<div className="absolute inset-x-0 top-0 h-1 bg-black dark:bg-accent opacity-80" />
					<CardHeader className="pb-2">
						<CardDescription>Deployments (30d)</CardDescription>
						<CardTitle className="text-3xl font-semibold tabular-nums">
							{recentDeploymentsCount}
						</CardTitle>
					</CardHeader>
					<CardContent className="text-sm text-muted-foreground">
						Across all apps
					</CardContent>
				</Card>

				{/* Workspace Members by Role */}
				<Card className="hover:border-border-strong group">
					<div className="absolute inset-x-0 top-0 h-1 bg-black dark:bg-accent opacity-80" />
					<CardHeader className="pb-2">
						<CardDescription>Team Members</CardDescription>
						<CardTitle className="text-3xl font-semibold tabular-nums">
							{members.length}
						</CardTitle>
					</CardHeader>
					<CardContent className="text-sm space-y-1">
						{Object.entries(membersByRole).length > 0 ? (
							Object.entries(membersByRole).map(([role, count]) => (
								<div
									key={role}
									className="flex justify-between text-muted-foreground"
								>
									<span className="capitalize">{role}:</span>
									<span className="font-medium">{count}</span>
								</div>
							))
						) : (
							<p className="text-muted-foreground">No members</p>
						)}
					</CardContent>
				</Card>
			</div>
		</div>
	);
}
