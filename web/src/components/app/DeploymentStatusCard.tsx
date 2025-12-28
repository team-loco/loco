import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { scaleResource } from "@/gen/resource/v1";
import { DeploymentPhase, type Deployment } from "@/gen/deployment/v1/deployment_pb";
import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { PHASE_COLOR_MAP } from "@/lib/deployment-constants";

interface DeploymentStatusCardProps {
	appId: string;
	deployment?: Deployment;
	isLoading?: boolean;
}

export function DeploymentStatusCard({
	appId,
	deployment,
	isLoading = false,
}: DeploymentStatusCardProps) {
	const [isEditing, setIsEditing] = useState(false);
	const [replicas, setReplicas] = useState(deployment?.replicas ?? 1);

	const scaleResourceMutation = useMutation(scaleResource);

	const hasChanges = replicas !== deployment?.replicas;

	const handleApply = async () => {
		try {
			await scaleResourceMutation.mutateAsync({
				resourceId: BigInt(appId),
				replicas,
			});
			setIsEditing(false);
		} catch (error) {
			console.error("Failed to scale resource:", error);
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

	if (!deployment) {
		return null;
	}

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
						<Badge
							variant="default"
							className={`text-xs ${PHASE_COLOR_MAP[deployment.status]}`}
						>
							{DeploymentPhase[deployment.status] || "unknown"}
						</Badge>
					</div>

					<div className="flex items-center justify-between">
						<span className="text-sm font-medium text-foreground">
							Replicas
						</span>
						<span className="text-sm font-mono">
							{deployment.replicas}/{deployment.replicas}
						</span>
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
								disabled={!hasChanges || scaleResourceMutation.isPending}
								className="flex-1"
							>
								{scaleResourceMutation.isPending ? "Applying..." : "Apply"}
							</Button>
						</div>
					</div>
				)}
			</CardContent>
		</Card>
	);
}
