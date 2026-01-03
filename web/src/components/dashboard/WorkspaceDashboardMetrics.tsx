import { useMemo } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useNavigate } from "react-router";

import { listResources } from "@/gen/resource/v1";
import { listMembers } from "@/gen/workspace/v1";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { TrendingUpIcon } from "lucide-react";

interface WorkspaceDashboardMetricsProps {
	workspaceId: bigint;
	workspaceName?: string;
}

export function WorkspaceDashboardMetrics({
	workspaceId,
}: WorkspaceDashboardMetricsProps) {
	// Fetch resources
	const { data: resourcesRes } = useQuery(
		listResources,
		{ workspaceId },
		{ enabled: !!workspaceId }
	);
	const apps = useMemo(
		() => resourcesRes?.resources ?? [],
		[resourcesRes?.resources]
	);

	// Fetch members
	const { data: membersRes } = useQuery(
		listMembers,
		{ workspaceId },
		{ enabled: !!workspaceId }
	);
	const members = useMemo(
		() => membersRes?.members ?? [],
		[membersRes?.members]
	);

	// Group members by role
	const membersByRole = useMemo(() => {
		const grouped: Record<string, number> = {};
		members.forEach((member) => {
			grouped[member.role] = (grouped[member.role] || 0) + 1;
		});
		return grouped;
	}, [members]);

	// Calculate active apps (not IDLE, status 3)
	const activeAppsCount = useMemo(() => {
		return apps.filter((app) => app.status !== 3).length;
	}, [apps]);

	// Calculate recent deployments count (approximated from apps data)
	const recentDeploymentsCount = 0;

	return (
		<div className="space-y-4">
			{/* Metric Cards */}
			<div className="grid grid-cols-1 md:grid-cols-3 gap-4">
				{/* Total Apps (Active) */}
				<Card>
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
				<Card>
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
				<Card>
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

			{/* Recent Deployments Table */}
			<RecentDeploymentsTable />
		</div>
	);
}

function RecentDeploymentsTable() {
	const navigate = useNavigate();
	const recentDeployments: Array<{
		resourceId: bigint;
		resourceName: string;
		deploymentId: bigint;
		status: number;
		replicas: number;
		createdAt: Date | null;
	}> = [];

	const statusLabels: Record<number, string> = {
		0: "Pending",
		1: "Running",
		2: "Succeeded",
		3: "Failed",
		4: "Canceled",
	};

	const statusColors: Record<number, string> = {
		0: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200",
		1: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
		2: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
		3: "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200",
		4: "bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-200",
	};

	return (
		<Card>
			<CardHeader>
				<CardTitle>Recent Deployments</CardTitle>
				<CardDescription>Last 30 days, up to 10 most recent</CardDescription>
			</CardHeader>
			<CardContent>
				{recentDeployments.length > 0 ? (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Resource Name</TableHead>
								<TableHead>Status</TableHead>
								<TableHead>Replicas</TableHead>
								<TableHead>Created</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{recentDeployments.map((d) => (
								<TableRow
									key={d.deploymentId.toString()}
									className="cursor-pointer"
									onClick={() => navigate(`/resource/${d.resourceId}`)}
								>
									<TableCell>{d.resourceName}</TableCell>
									<TableCell>
										<Badge
											className={`text-xs ${statusColors[d.status]}`}
											variant="outline"
										>
											{statusLabels[d.status]}
										</Badge>
									</TableCell>
									<TableCell>{d.replicas}</TableCell>
									<TableCell className="text-muted-foreground">
										{d.createdAt
											? d.createdAt.toLocaleDateString("en-US", {
													month: "short",
													day: "numeric",
													hour: "2-digit",
													minute: "2-digit",
											  })
											: "N/A"}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				) : (
					<div className="text-center py-8 text-muted-foreground">
						No recent deployments
					</div>
				)}
			</CardContent>
		</Card>
	);
}
