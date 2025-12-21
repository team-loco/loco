import { useMemo } from "react";
import { useQuery } from "@connectrpc/connect-query";

import { listResources } from "@/gen/resource/v1";
import { listDeployments } from "@/gen/deployment/v1";
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
	const apps = useMemo(() => resourcesRes?.resources ?? [], [resourcesRes?.resources]);

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

	// Fetch deployments for all apps
	const deploymentsQueries = apps.map((app) =>
		useQuery(
			listDeployments,
			{ appId: app.id, limit: 100 },
			{ enabled: !!app.id }
		)
	);

	// Count recent deployments (last 30 days)
	const recentDeploymentsCount = useMemo(() => {
		const now = new Date();
		const thirtyDaysAgo = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);

		let count = 0;
		deploymentsQueries.forEach((query) => {
			query.data?.deployments?.forEach((deployment) => {
				const createdAt = deployment.createdAt
					? new Date(
							typeof deployment.createdAt === "string"
								? deployment.createdAt
								: deployment.createdAt
					  )
					: null;
				if (createdAt && createdAt >= thirtyDaysAgo) {
					count++;
				}
			});
		});
		return count;
	}, [deploymentsQueries]);

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
			<RecentDeploymentsTable apps={apps} />
		</div>
	);
}

function RecentDeploymentsTable({ apps }: { apps: any[] }) {
	// Fetch deployments for all apps
	const deploymentsQueries = apps.map((app) =>
		useQuery(
			listDeployments,
			{ appId: app.id, limit: 20 },
			{ enabled: !!app.id }
		)
	);

	// Combine and sort recent deployments
	const recentDeployments = useMemo(() => {
		const all: Array<{
			appId: bigint;
			appName: string;
			deploymentId: bigint;
			status: number;
			replicas: number;
			createdAt: Date | null;
		}> = [];

		apps.forEach((app, idx) => {
			const deployments = deploymentsQueries[idx]?.data?.deployments ?? [];
			deployments.forEach((d: any) => {
				all.push({
					appId: app.id,
					appName: app.name,
					deploymentId: d.id,
					status: d.status,
					replicas: d.replicas,
					createdAt: d.createdAt
						? new Date(
								typeof d.createdAt === "string" ? d.createdAt : d.createdAt
						  )
						: null,
				});
			});
		});

		// Filter last 30 days and sort by date
		const now = new Date();
		const thirtyDaysAgo = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);

		return all
			.filter((d) => d.createdAt && d.createdAt >= thirtyDaysAgo)
			.sort(
				(a, b) => (b.createdAt?.getTime() ?? 0) - (a.createdAt?.getTime() ?? 0)
			)
			.slice(0, 10);
	}, [apps, deploymentsQueries]);

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
								<TableHead>App Name</TableHead>
								<TableHead>Status</TableHead>
								<TableHead>Replicas</TableHead>
								<TableHead>Created</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{recentDeployments.map((d) => (
								<TableRow key={d.deploymentId.toString()}>
									<TableCell>{d.appName}</TableCell>
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
