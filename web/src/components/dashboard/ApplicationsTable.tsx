import { useNavigate } from "react-router";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
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
import { hoursAgo } from "@/lib/format-time";
import { getTableMetrics } from "@/lib/mock-metrics";

interface ApplicationsTableProps {
	resources: Resource[];
	workspaceId?: string;
}

export function ApplicationsTable({
	resources,
	workspaceId,
}: ApplicationsTableProps) {
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
		<div className="border border-border rounded-xl shadow-sm bg-card overflow-hidden">
			<div className="px-6 py-4 border-b border-border bg-muted/30">
				<h2 className="text-lg font-semibold">Applications</h2>
			</div>

			<div className="overflow-x-auto">
				<Table>
					<TableHeader>
						<TableRow className="border-b border-border hover:bg-transparent">
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
								const metrics = getTableMetrics(resource);
								const status = getStatusLabel(resource.status);
								// eslint-disable-next-line @typescript-eslint/no-unsafe-enum-comparison
								const isActive = resource.status !== 3;

								return (
									<TableRow
										key={resource.id}
										onClick={() => { handleRowClick(resource.id); }}
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
																width: `${metrics.cpu.toString()}%`,
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
												{resource.createdAt ? hoursAgo(resource.createdAt) : "never"}
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
