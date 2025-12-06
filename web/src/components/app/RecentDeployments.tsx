import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import type { Deployment } from "@/gen/deployment/v1/deployment_pb";
import { ChevronDown, ChevronUp, RotateCcw } from "lucide-react";
import { useState } from "react";

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
			const now = new Date().getTime();
			const diff = now - ms;
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
		return null;
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
							<TableHead>Image</TableHead>
							<TableHead>Status</TableHead>
							<TableHead>Created</TableHead>
							<TableHead className="text-right">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{deployments.map((deployment) => (
							<div key={deployment.id}>
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
										{deployment.image?.split("/").pop() || "—"}
									</TableCell>
									<TableCell>
										<Badge variant="secondary" className="text-xs">
											{deployment.status || "unknown"}
										</Badge>
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
									<TableRow className="bg-background/30">
										<TableCell colSpan={5}>
											<div className="p-4 space-y-2">
												<div className="grid grid-cols-2 gap-4">
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
															{deployment.image}
														</p>
													</div>
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
														<p className="text-sm">{deployment.status}</p>
													</div>
												</div>

												{/* Config Preview */}
												{deployment.config && (
													<div>
														<p className="text-xs text-foreground opacity-60 uppercase mt-2">
															Config
														</p>
														<pre className="text-xs bg-foreground/5 border border-border rounded p-2 mt-1 overflow-auto max-h-40">
															{deployment.config}
														</pre>
													</div>
												)}
											</div>
										</TableCell>
									</TableRow>
								)}
							</div>
						))}
					</TableBody>
				</Table>
			</CardContent>
		</Card>
	);
}
