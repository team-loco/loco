import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { scaleApp } from "@/gen/app/v1";
import type { AppStatus } from "@/gen/app/v1/app_pb";
import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";

interface DeploymentStatusCardProps {
	appId: string;
	status: AppStatus | null;
	isLoading?: boolean;
}

export function DeploymentStatusCard({
	appId,
	status,
	isLoading = false,
}: DeploymentStatusCardProps) {
	const [isEditing, setIsEditing] = useState(false);
	const [replicas, setReplicas] = useState(status?.replicas ?? 1);
	const [cpu, setCpu] = useState(status?.resources?.limits?.cpu || "1000m");
	const [memory, setMemory] = useState(
		status?.resources?.limits?.memory || "512Mi"
	);

	const scaleAppMutation = useMutation(scaleApp);

	const hasChanges =
		replicas !== status?.replicas ||
		cpu !== status?.resources?.limits?.cpu ||
		memory !== status?.resources?.limits?.memory;

	const handleApply = async () => {
		try {
			await scaleAppMutation.mutateAsync({
				appId,
				replicas,
				cpu,
				memory,
			});
			setIsEditing(false);
		} catch (error) {
			console.error("Failed to scale app:", error);
		}
	};

	if (isLoading) {
		return (
			<Card className="animate-pulse">
				<CardContent className="p-6 space-y-4">
					<div className="h-6 bg-main/20 rounded w-1/4"></div>
					<div className="h-4 bg-main/10 rounded w-1/2"></div>
				</CardContent>
			</Card>
		);
	}

	if (!status) {
		return null;
	}

	const statusColor =
		status.status === "running"
			? "green"
			: status.status === "building" || status.status === "deploying"
			? "blue"
			: "red";

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle className="flex items-center justify-between">
					<span>Deployment Status</span>
					{!isEditing && (
						<Button
							variant="outline"
							size="sm"
							onClick={() => setIsEditing(true)}
							className="border-2"
						>
							Scale
						</Button>
					)}
				</CardTitle>
			</CardHeader>
			<CardContent className="space-y-6">
				{/* Current Status */}
				<div className="space-y-3">
					<div className="flex items-center justify-between">
						<span className="text-sm font-medium text-foreground">Status</span>
						<Badge variant="secondary" className="bg-blue-100">
							{status.status || "unknown"}
						</Badge>
					</div>

					<div className="flex items-center justify-between">
						<span className="text-sm font-medium text-foreground">Image</span>
						<span className="text-xs text-foreground opacity-70 font-mono">
							{status.image?.substring(0, 20)}...
						</span>
					</div>

					<div className="flex items-center justify-between">
						<span className="text-sm font-medium text-foreground">
							Replicas
						</span>
						<span className="text-sm font-mono">
							{status.replicas}/{status.replicas}
						</span>
					</div>
				</div>

				{/* Resource Usage */}
				<div className="space-y-3 border-t border-border pt-4">
					<div>
						<div className="flex justify-between mb-2">
							<span className="text-sm font-medium text-foreground">CPU</span>
							<span className="text-xs text-foreground opacity-60">
								450m / {status.resources?.limits?.cpu || "1000m"}
							</span>
						</div>
						<div className="w-full bg-background border-2 border-border rounded h-2">
							<div
								className="bg-main h-full rounded"
								style={{ width: "45%" }}
							></div>
						</div>
					</div>

					<div>
						<div className="flex justify-between mb-2">
							<span className="text-sm font-medium text-foreground">
								Memory
							</span>
							<span className="text-xs text-foreground opacity-60">
								256Mi / {status.resources?.limits?.memory || "512Mi"}
							</span>
						</div>
						<div className="w-full bg-background border-2 border-border rounded h-2">
							<div
								className="bg-main h-full rounded"
								style={{ width: "50%" }}
							></div>
						</div>
					</div>
				</div>

				{/* Scale Controls */}
				{isEditing && (
					<div className="space-y-4 border-t border-border pt-4">
						<div>
							<label className="text-sm font-medium text-foreground">
								Replicas
							</label>
							<Input
								type="number"
								min="1"
								max="10"
								value={replicas}
								onChange={(e) => setReplicas(parseInt(e.target.value) || 1)}
								className="mt-1"
							/>
						</div>

						<div>
							<label className="text-sm font-medium text-foreground">
								CPU (e.g., 1000m)
							</label>
							<Input
								type="text"
								value={cpu}
								onChange={(e) => setCpu(e.target.value)}
								className="mt-1 font-mono text-sm"
							/>
						</div>

						<div>
							<label className="text-sm font-medium text-foreground">
								Memory (e.g., 512Mi)
							</label>
							<Input
								type="text"
								value={memory}
								onChange={(e) => setMemory(e.target.value)}
								className="mt-1 font-mono text-sm"
							/>
						</div>

						<div className="flex gap-2 pt-4">
							<Button
								variant="outline"
								onClick={() => setIsEditing(false)}
								className="flex-1"
							>
								Cancel
							</Button>
							<Button
								onClick={handleApply}
								disabled={!hasChanges || scaleAppMutation.isPending}
								className="flex-1"
							>
								{scaleAppMutation.isPending ? "Applying..." : "Apply"}
							</Button>
						</div>
					</div>
				)}
			</CardContent>
		</Card>
	);
}
