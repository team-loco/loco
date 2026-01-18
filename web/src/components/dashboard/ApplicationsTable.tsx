import { useNavigate } from "react-router";
import type { Resource } from "@/gen/loco/resource/v1/resource_pb";
import { getStatusLabel } from "@/lib/app-status";
import { StatusBadge } from "@/components/StatusBadge";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";

interface ApplicationsTableProps {
	resources: Resource[];
	workspaceId?: bigint;
}

// Mock data generator for resources we don't have metrics for
function getMockMetrics(resourceId: bigint) {
	const seed = Number(resourceId);
	return {
		cpu: Math.floor((Math.sin(seed) * 0.5 + 0.5) * 100),
		memory: Math.floor((Math.cos(seed) * 0.5 + 0.5) * 1000) + 256,
		requests: `${Math.floor((Math.sin(seed * 2) * 0.5 + 0.5) * 900)}K/day`,
		uptime: "99.9%",
		replicas: Math.floor((Math.cos(seed * 3) * 0.5 + 0.5) * 5) + 1,
	};
}

function getLastDeployedText(createdAt: any): string {
	if (!createdAt) return "never";

	try {
		let timestamp: number;
		if (typeof createdAt === "object" && "seconds" in createdAt) {
			timestamp = Number(createdAt.seconds) * 1000;
		} else if (typeof createdAt === "number") {
			timestamp = createdAt;
		} else {
			return "unknown";
		}

		const now = new Date().getTime();
		const diff = now - timestamp;
		const hours = Math.floor(diff / (1000 * 60 * 60));
		const days = Math.floor(diff / (1000 * 60 * 60 * 24));

		if (hours === 0) return "just now";
		if (hours === 1) return "1h ago";
		if (hours < 24) return `${hours}h ago`;
		if (days === 1) return "1d ago";
		return `${days}d ago`;
	} catch {
		return "unknown";
	}
}

export function ApplicationsTable({
	resources,
	workspaceId,
}: ApplicationsTableProps) {
	const navigate = useNavigate();

	const handleRowClick = (resourceId: bigint) => {
		navigate(
			`/resource/${resourceId}${workspaceId ? `?workspace=${workspaceId}` : ""}`
		);
	};

	return (
		<div className="border-2 border-black dark:border-neutral-700 rounded-2xl shadow-[4px_4px_0px_0px_#000] bg-card overflow-hidden">
			<div className="px-6 py-4 border-b-2 border-black dark:border-neutral-700 bg-muted/30">
				<h2 className="text-lg font-semibold">Applications</h2>
			</div>

			<div className="overflow-x-auto">
				<Table>
					<TableHeader>
						<TableRow className="border-b-2 border-black dark:border-neutral-700 hover:bg-transparent">
							<TableHead className="px-6 py-3 font-semibold text-foreground">
								Application
							</TableHead>
							<TableHead className="px-6 py-3 font-semibold text-foreground">
								Status
							</TableHead>
							<TableHead className="px-6 py-3 font-semibold text-foreground">
								Resources
							</TableHead>
							<TableHead className="px-6 py-3 font-semibold text-foreground">
								Traffic
							</TableHead>
							<TableHead className="px-6 py-3 font-semibold text-foreground">
								Uptime
							</TableHead>
							<TableHead className="px-6 py-3 font-semibold text-foreground">
								Last Deploy
							</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{resources.length > 0 ? (
							resources.map((resource) => {
								const metrics = getMockMetrics(resource.id);
								const status = getStatusLabel(resource.status);
								const isActive = resource.status !== 3;

								return (
									<TableRow
										key={resource.id.toString()}
										onClick={() => handleRowClick(resource.id)}
										className="cursor-pointer border-b border-border last:border-0 hover:bg-muted/50 transition-colors"
									>
										<TableCell className="px-6 py-4">
											<div className="flex items-center gap-3">
												<div className="relative">
													<div
														className={`w-2 h-2 rounded-full ${
															isActive
																? "bg-emerald-500 dark:bg-emerald-400"
																: "bg-muted-foreground"
														}`}
													></div>
													{isActive && (
														<div className="absolute inset-0 w-2 h-2 rounded-full bg-emerald-500 dark:bg-emerald-400 animate-[ping_2s_ease-in-out_infinite]"></div>
													)}
												</div>
												<div>
													<div className="font-mono font-medium">
														{resource.name}
													</div>
													<div className="text-xs text-muted-foreground">
														{metrics.replicas} replicas
													</div>
												</div>
											</div>
										</TableCell>
										<TableCell className="px-6 py-4">
											<StatusBadge status={status} showTooltip={false} />
										</TableCell>
										<TableCell className="px-6 py-4">
											<div className="space-y-1">
												<div className="flex items-center gap-2 text-xs">
													<span className="text-muted-foreground">CPU:</span>
													<div className="flex-1 max-w-[60px] h-2 bg-muted border border-border rounded-sm overflow-hidden">
														<div
															className={`h-full transition-all ${
																metrics.cpu > 80
																	? "bg-red-500"
																	: metrics.cpu > 60
																	? "bg-amber-500"
																	: "bg-emerald-500"
															}`}
															style={{
																width: `${metrics.cpu}%`,
															}}
														></div>
													</div>
													<span className="text-foreground font-mono font-medium">
														{metrics.cpu}%
													</span>
												</div>
												<div className="text-xs text-muted-foreground">
													{metrics.memory}MB
												</div>
											</div>
										</TableCell>
										<TableCell className="px-6 py-4">
											<div className="text-sm font-mono font-medium">
												{metrics.requests}
											</div>
										</TableCell>
										<TableCell className="px-6 py-4">
											<div className="text-sm font-mono font-medium text-emerald-600 dark:text-emerald-400">
												{metrics.uptime}
											</div>
										</TableCell>
										<TableCell className="px-6 py-4">
											<div className="text-sm text-muted-foreground">
												{getLastDeployedText(resource.createdAt)}
											</div>
										</TableCell>
									</TableRow>
								);
							})
						) : (
							<TableRow>
								<TableCell colSpan={6} className="h-24 text-center">
									<div className="text-muted-foreground">
										No applications found
									</div>
								</TableCell>
							</TableRow>
						)}
					</TableBody>
				</Table>
			</div>
		</div>
	);
}
