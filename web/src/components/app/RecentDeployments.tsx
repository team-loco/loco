import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
	DeploymentPhase,
	type Deployment,
} from "@/gen/deployment/v1/deployment_pb";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { ChevronDown, ChevronUp, RotateCcw } from "lucide-react";
import React, { useState } from "react";
import { PHASE_COLOR_MAP } from "@/lib/deployment-constants";

interface RecentDeploymentsProps {
	deployments: Deployment[];
	appId?: string;
	isLoading?: boolean;
}

export function RecentDeployments({
	deployments,
	isLoading = false,
}: RecentDeploymentsProps) {
	const [expandedId, setExpandedId] = useState<bigint | null>(null);

	const formatTimestamp = (timestamp: unknown): string => {
		if (!timestamp) return "unknown";
		try {
			let ms: number;
			if (typeof timestamp === "object" && "seconds" in timestamp) {
				ms = Number((timestamp as Record<string, unknown>).seconds) * 1000;
			} else if (typeof timestamp === "number") {
				ms = timestamp;
			} else {
				return "unknown";
			}
			const date = new Date(ms);
			return date.toLocaleDateString("en-US", {
				month: "short",
				day: "numeric",
				hour: "2-digit",
				minute: "2-digit",
				hour12: true,
			});
		} catch {
			return "unknown";
		}
	};

	const getPhaseColor = (phase: DeploymentPhase): string => {
		return PHASE_COLOR_MAP[phase];
	};

	if (isLoading) {
		return (
			<Card className="animate-pulse">
				<CardContent className="p-6">
					<div className="h-6 bg-main/20 rounded w-1/4"></div>
				</CardContent>
			</Card>
		);
	}

	if (deployments.length === 0) {
		return (
			<Card className="border-2">
				<CardHeader>
					<CardTitle>Recent Deployments</CardTitle>
				</CardHeader>
				<CardContent>
					<p className="text-sm text-foreground opacity-70">
						No deployments yet
					</p>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle>Recent Deployments ({deployments.length})</CardTitle>
			</CardHeader>
			<CardContent>
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead></TableHead>
							<TableHead>Deployment ID</TableHead>
							<TableHead>Status</TableHead>
							<TableHead>Replicas</TableHead>
							<TableHead>Created</TableHead>
							<TableHead className="text-right">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{deployments.map((deployment) => (
							<React.Fragment key={deployment.id}>
								<TableRow className="cursor-pointer hover:bg-background/50">
									<TableCell
										onClick={() =>
											setExpandedId(
												expandedId === deployment.id ? null : deployment.id
											)
										}
										className="w-8 p-0"
									>
										{expandedId === deployment.id ? (
											<ChevronUp className="w-4 h-4" />
										) : (
											<ChevronDown className="w-4 h-4" />
										)}
									</TableCell>
									<TableCell className="font-mono text-xs max-w-xs truncate">
										{deployment.id}
									</TableCell>
									<TableCell>
										<Badge
											variant="default"
											className={`text-xs ${getPhaseColor(deployment.status)}`}
										>
											{DeploymentPhase[deployment.status]}
										</Badge>
									</TableCell>
									<TableCell className="text-sm">
										{deployment.replicas || "—"}
									</TableCell>
									<TableCell className="text-sm text-foreground opacity-70">
										{formatTimestamp(deployment.createdAt)}
									</TableCell>
									<TableCell className="text-right">
										<Button
											variant="secondary"
											size="sm"
											className="h-8"
											onClick={(e) => {
												e.stopPropagation();
												// TODO: Implement rollback
											}}
										>
											<RotateCcw className="w-4 h-4" />
										</Button>
									</TableCell>
								</TableRow>

								{/* Expanded Details */}
								{expandedId === deployment.id && (
									<TableRow
										key={`${deployment.id}-expanded`}
										className="bg-background/30"
									>
										<TableCell colSpan={6}>
											<div className="p-4 space-y-3">
												<div>
													<p className="text-xs text-foreground opacity-60 uppercase">
														ID
													</p>
													<p className="text-sm font-mono">{deployment.id}</p>
												</div>
												<div>
													<p className="text-xs text-foreground opacity-60 uppercase">
														Image
													</p>
													<p className="text-sm font-mono break-all">
														{deployment.image || "—"}
													</p>
												</div>
												<div className="grid grid-cols-2 gap-4">
													<div>
														<p className="text-xs text-foreground opacity-60 uppercase">
															Replicas
														</p>
														<p className="text-sm">
															{deployment.replicas || "—"}
														</p>
													</div>
													<div>
														<p className="text-xs text-foreground opacity-60 uppercase">
															Status
														</p>
														<p className="text-sm">
															{DeploymentPhase[deployment.status]}
														</p>
													</div>
												</div>
												{deployment.message && (
													<div>
														<p className="text-xs text-foreground opacity-60 uppercase">
															Message
														</p>
														<p className="text-sm break-all">
															{deployment.message}
														</p>
													</div>
												)}
											</div>
										</TableCell>
									</TableRow>
								)}
							</React.Fragment>
						))}
					</TableBody>
				</Table>
			</CardContent>
		</Card>
	);
}
