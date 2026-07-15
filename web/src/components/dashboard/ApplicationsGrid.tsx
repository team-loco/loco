import { Button } from "@/components/design/Button";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { Resource } from "@/gen/loco/resource/v1/resource_pb";
import {
    Activity,
    Cpu,
    ExternalLink,
    GitBranch,
    GitFork,
    Settings
} from "lucide-react";
import { useNavigate } from "react-router";

interface ApplicationsGridProps {
	resources: Resource[];
	workspaceId?: string;
}

// Reuse mock metrics generator
function getMockMetrics(resourceId: string) {
	let seed = 0;
	for (let i = 0; i < resourceId.length; i++) seed += resourceId.charCodeAt(i);
	return {
		cpu: Math.floor((Math.sin(seed) * 0.5 + 0.5) * 100),
		memory: Math.floor((Math.cos(seed) * 0.5 + 0.5) * 1000) + 256,
		requests: `${Math.floor((Math.sin(seed * 2) * 0.5 + 0.5) * 900).toString()}K`,
		uptime: "99.9%",
		commit: Math.random().toString(36).substring(2, 9),
		branch: "main",
	};
}

function getLastDeployedText(createdAt: any): string {
	if (!createdAt) return "Never deployed";

	try {
		let timestamp: number;
		if (typeof createdAt === "object" && "seconds" in createdAt) {
			timestamp = Number(createdAt.seconds) * 1000;
		} else if (typeof createdAt === "number") {
			timestamp = createdAt;
		} else {
			return "Unknown status";
		}

		const now = new Date().getTime();
		const diff = now - timestamp;
		const hours = Math.floor(diff / (1000 * 60 * 60));
		const days = Math.floor(diff / (1000 * 60 * 60 * 24));

		if (hours === 0) return "Deployed just now";
		if (hours === 1) return "Deployed 1h ago";
		if (hours < 24) return `Deployed ${hours.toString()}h ago`;
		if (days === 1) return "Deployed 1d ago";
		return `Deployed ${days.toString()}d ago`;
	} catch {
		return "Unknown status";
	}
}

export function ApplicationsGrid({
	resources,
	workspaceId,
}: ApplicationsGridProps) {
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const orgId = activeOrgId;
	const wsId = activeWorkspaceId ?? workspaceId;

	const handleRowClick = (resourceId: string) => {
		if (orgId && wsId) {
			void navigate(`/org/${orgId}/wks/${wsId}/resource/${resourceId}`);
		}
	};

	return (
		<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
			{resources.length > 0 ? (
				resources.map((resource) => {
					const metrics = getMockMetrics(resource.id);
					// eslint-disable-next-line @typescript-eslint/no-unsafe-enum-comparison
					const isActive = resource.status !== 3; // Example check

					return (
						<div
							key={resource.id}
							onClick={() => { handleRowClick(resource.id); }}
							className="group relative flex flex-col bg-card border border-border rounded-xl shadow-xs hover:shadow-sm hover:border-border-strong cursor-pointer transition-all duration-medium overflow-hidden"
						>
							{/* Card Header */}
							<div className="p-5 border-b border-border bg-muted/10">
								<div className="flex items-start justify-between mb-3">
									<div className="flex items-center gap-3">
										<div className="w-10 h-10 rounded-lg bg-orange-100 dark:bg-orange-900/30 text-orange-600 flex items-center justify-center font-bold text-lg">
											{resource.name.substring(0, 1).toUpperCase()}
										</div>
										<div>
											<h3 className="font-semibold text-foreground flex items-center gap-2">
												{resource.name}
											</h3>
											<a
												href="#repo"
												onClick={(e) => { e.stopPropagation(); }}
												className="text-xs text-muted-foreground hover:text-foreground flex items-center gap-1 mt-0.5"
											>
												<GitFork className="w-3 h-3" />
												team-loco/{resource.name}
											</a>
										</div>
									</div>
									<Button 
										variant="ghost" 
										size="icon" 
										className="h-8 w-8 -mr-2 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity"
										onClick={(e) => { e.stopPropagation(); }}
									>
										<Settings className="w-4 h-4" />
									</Button>
								</div>

								{/* App Domain & Status Link */}
								<div className="flex items-center gap-2 text-sm">
									<a
										href={`https://${resource.name}.loco.app`}
										target="_blank"
										rel="noreferrer"
										onClick={(e) => { e.stopPropagation(); }}
										className="text-foreground hover:text-primary transition-colors flex items-center gap-1.5 font-medium"
									>
										{resource.name}.loco.app
										<ExternalLink className="w-3.5 h-3.5 text-muted-foreground" />
									</a>
								</div>
							</div>

							{/* Card Body - Metrics area */}
							<div className="p-5 flex-1 flex flex-col gap-4">
								<div className="grid grid-cols-2 gap-4">
									{/* CPU Metric Column */}
									<div className="space-y-1.5">
										<div className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
											<Cpu className="w-3.5 h-3.5" /> CPU
										</div>
										<div className="flex items-center gap-2">
											<div className="flex-1 h-1.5 bg-muted rounded-full overflow-hidden">
												<div
													className={`h-full ${
														metrics.cpu > 80
															? "bg-red-500"
															: metrics.cpu > 60
															? "bg-amber-500"
															: "bg-emerald-500"
													}`}
													style={{ width: `${metrics.cpu.toString()}%` }}
												/>
											</div>
											<span className="text-xs font-mono font-medium">
												{metrics.cpu}%
											</span>
										</div>
									</div>

									{/* Traffic Metric Column */}
									<div className="space-y-1.5">
										<div className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
											<Activity className="w-3.5 h-3.5" /> Traffic
										</div>
										<div className="text-sm font-mono font-medium text-foreground">
											{metrics.requests}/d
										</div>
									</div>
								</div>
							</div>

							{/* Card Footer */}
							<div className="p-4 px-5 bg-muted/20 border-t border-border flex items-center justify-between text-xs text-muted-foreground">
								<div className="flex items-center gap-2">
									<div className="relative flex items-center justify-center">
										<div
											className={`w-2 h-2 rounded-full ${
												isActive
													? "bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.4)]"
													: "bg-muted-foreground"
											}`}
										/>
									</div>
									<span>{getLastDeployedText(resource.createdAt)}</span>
								</div>
								
								<div className="flex items-center gap-1.5 bg-background border border-border px-2 py-0.5 rounded-md font-mono text-[10px]">
									<GitBranch className="w-3 h-3" />
									{metrics.branch}
								</div>
							</div>
						</div>
					);
				})
			) : (
				<div className="col-span-full py-12 text-center border-2 border-dashed border-border rounded-xl">
					<div className="text-muted-foreground">No applications found</div>
				</div>
			)}
		</div>
	);
}
