import { useMemo } from "react";
import { useNavigate } from "react-router";
import { CheckCircle, XCircle } from "lucide-react";
import type { Resource } from "@/gen/resource/v1/resource_pb";

interface RecentDeploymentsProps {
	resources: Resource[];
	workspaceId?: bigint;
}

interface MockDeployment {
	id: string;
	resourceId: bigint;
	resourceName: string;
	status: "success" | "failed";
	commit: string;
	duration: string;
	time: string;
}

// Generate mock deployment data from resources
function generateMockDeployments(resources: Resource[]): MockDeployment[] {
	const deployments: MockDeployment[] = [];

	resources.forEach((resource) => {
		const seed = Number(resource.id);
		const isSuccess = Math.sin(seed) > 0;
		const hoursAgo = Math.floor((Math.cos(seed * 2) * 0.5 + 0.5) * 48) + 1;

		// Generate 1-2 deployments per resource
		const numDeployments = Math.floor((Math.sin(seed * 3) * 0.5 + 0.5) * 2) + 1;

		for (let i = 0; i < numDeployments; i++) {
			const adjustedHoursAgo = hoursAgo + i * 12;
			const minutes = Math.floor((Math.sin(seed * (i + 1)) * 0.5 + 0.5) * 5) + 1;
			const seconds = Math.floor((Math.cos(seed * (i + 1)) * 0.5 + 0.5) * 60);

			deployments.push({
				id: `${resource.id}-${i}`,
				resourceId: resource.id,
				resourceName: resource.name,
				status: i === 0 ? (isSuccess ? "success" : "failed") : "success",
				commit: `${Math.random().toString(36).substring(2, 9)}`,
				duration: `${minutes}m ${seconds}s`,
				time: formatTimeAgo(adjustedHoursAgo),
			});
		}
	});

	// Sort by time (most recent first) and take top 5
	return deployments
		.sort((a, b) => {
			const aTime = parseTimeAgo(a.time);
			const bTime = parseTimeAgo(b.time);
			return aTime - bTime;
		})
		.slice(0, 5);
}

function parseTimeAgo(timeStr: string): number {
	if (timeStr.includes("h ago")) {
		return parseInt(timeStr);
	}
	if (timeStr.includes("d ago")) {
		return parseInt(timeStr) * 24;
	}
	return 0;
}

function formatTimeAgo(hours: number): string {
	if (hours === 0) return "just now";
	if (hours === 1) return "1h ago";
	if (hours < 24) return `${hours}h ago`;
	const days = Math.floor(hours / 24);
	if (days === 1) return "1d ago";
	return `${days}d ago`;
}

function DeploymentStatusIcon({ status }: { status: "success" | "failed" }) {
	if (status === "success") {
		return (
			<div className="p-2 bg-emerald-100 dark:bg-emerald-950 border-2 border-black dark:border-neutral-700 rounded-2xl shadow-[2px_2px_0px_0px_#000]">
				<CheckCircle className="w-4 h-4 text-emerald-900 dark:text-emerald-400" />
			</div>
		);
	}
	return (
		<div className="p-2 bg-red-100 dark:bg-red-950 border-2 border-black dark:border-neutral-700 rounded-2xl shadow-[2px_2px_0px_0px_#000]">
			<XCircle className="w-4 h-4 text-red-900 dark:text-red-400" />
		</div>
	);
}

export function RecentDeployments({ resources, workspaceId }: RecentDeploymentsProps) {
	const navigate = useNavigate();

	const recentDeployments = useMemo(() => {
		return generateMockDeployments(resources);
	}, [resources]);

	const handleViewApp = (resourceId: bigint) => {
		navigate(`/resource/${resourceId}${workspaceId ? `?workspace=${workspaceId}` : ""}`);
	};

	return (
		<div className="border-2 border-black dark:border-neutral-700 rounded-2xl shadow-[4px_4px_0px_0px_#000] bg-card overflow-hidden">
			<div className="px-6 py-4 border-b-2 border-black dark:border-neutral-700 bg-muted/30">
				<h2 className="text-lg font-semibold">Recent Deployments</h2>
			</div>
			<div className="divide-y divide-border">
				{recentDeployments.length > 0 ? (
					recentDeployments.map((deploy) => (
						<div
							key={deploy.id}
							className="px-6 py-4 hover:bg-muted/50 transition-colors cursor-pointer"
							onClick={() => handleViewApp(deploy.resourceId)}
						>
							<div className="flex items-center justify-between">
								<div className="flex items-center gap-4">
									<DeploymentStatusIcon status={deploy.status} />
									<div>
										<div className="font-mono font-medium">
											{deploy.resourceName}
										</div>
										<div className="text-xs text-muted-foreground mt-0.5">
											<span className="font-mono">{deploy.commit}</span> ·{" "}
											{deploy.duration} · {deploy.time}
										</div>
									</div>
								</div>
								<button
									onClick={(e) => {
										e.stopPropagation();
										handleViewApp(deploy.resourceId);
									}}
									className="text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
								>
									View app →
								</button>
							</div>
						</div>
					))
				) : (
					<div className="px-6 py-12 text-center text-muted-foreground">
						No recent deployments
					</div>
				)}
			</div>
		</div>
	);
}
