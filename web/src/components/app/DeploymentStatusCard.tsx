import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/design/Card";
import { Label } from "@/components/design/Label";
import { NumberInput } from "@/components/ui/number-input";
import { Slider } from "@/components/ui/slider";
import {
	Tooltip,
	TooltipContent,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/design/Tooltip";
import {
	DeploymentPhase,
	type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";
import { scaleResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { BADGE_COLOR_MAP, PHASE_COLOR_MAP } from "@/lib/deployment-constants";
import { getPhaseTooltip, getServiceSpec } from "@/lib/deployment-utils";
import { toastConnectError } from "@/lib/error-handler";
import { formatShortId } from "@/lib/utils";
import { useMutation } from "@connectrpc/connect-query";
import { Cpu, HardDrive } from "lucide-react";
import { useMemo, useState } from "react";
import { CPU_OPTIONS, MEMORY_OPTIONS } from "@/lib/resource-options";

interface DeploymentStatusCardProps {
	resourceId: string;
	deployment?: Deployment;
	isLoading?: boolean;
}

const cpuOptions = CPU_OPTIONS;
const memoryOptions = MEMORY_OPTIONS;

export function DeploymentStatusCard({
	resourceId,
	deployment,
	isLoading = false,
}: DeploymentStatusCardProps) {

	const { cpuIndex: initialCpuIndex, memoryIndex: initialMemoryIndex } =
		useMemo(() => {
			let cpu = "1000m";
			let memory = "512Mi";

			const service = deployment ? getServiceSpec(deployment) : undefined;
			if (service) {
				if (service.cpu) cpu = service.cpu;
				if (service.memory) memory = service.memory;
			}

			return {
				cpuIndex: cpuOptions.indexOf(cpu),
				memoryIndex: memoryOptions.indexOf(memory),
			};
		}, [deployment]);

	const [isEditing, setIsEditing] = useState(false);
	const [replicas, setReplicas] = useState(deployment?.replicas ?? 1);
	const [cpuIndex, setCpuIndex] = useState<number>(
		initialCpuIndex >= 0 ? initialCpuIndex : 3,
	);
	const [memoryIndex, setMemoryIndex] = useState<number>(
		initialMemoryIndex >= 0 ? initialMemoryIndex : 1,
	);

	const scaleResourceMutation = useMutation(scaleResource);

	const hasChanges = useMemo(() => {
		return (
			replicas !== deployment?.replicas ||
			cpuIndex !== (initialCpuIndex >= 0 ? initialCpuIndex : 3) ||
			memoryIndex !== (initialMemoryIndex >= 0 ? initialMemoryIndex : 1)
		);
	}, [
		replicas,
		cpuIndex,
		memoryIndex,
		deployment?.replicas,
		initialCpuIndex,
		initialMemoryIndex,
	]);

	const formatTimestamp = (timestamp: unknown): string => {
		if (!timestamp) return "—";
		try {
			let ms: number;
			if (typeof timestamp === "object" && "seconds" in timestamp) {
				ms = Number((timestamp as Record<string, unknown>).seconds) * 1000;
			} else if (typeof timestamp === "number") {
				ms = timestamp;
			} else {
				return "—";
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
			return "—";
		}
	};

	// todo: do this in a better way.
	const getImage = (deployment: Deployment): string => {
		const service = getServiceSpec(deployment);
		let image = service?.build?.image ?? "—";
		image = image.replace("registry.gitlab.com/locomotive-group/", "");
		return image;
	};

	const handleApply = async () => {
		try {
			await scaleResourceMutation.mutateAsync({
				resourceId: resourceId,
				replicas,
				cpu: cpuOptions[cpuIndex],
				memory: memoryOptions[memoryIndex],
			});
			setIsEditing(false);
		} catch (error) {
			toastConnectError(error, "Failed to scale resource");
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

	const phaseColors = PHASE_COLOR_MAP[deployment.status];
	const getBgColor = (): string => {
		const baseColors = phaseColors.split(" ");
		const bgColor = baseColors.find((c) => c.startsWith("bg-"));
		const bgMap: Record<string, string> = {
			"bg-gray-100":
				"border-gray-300 bg-gray-50 dark:bg-gray-950/30 dark:border-gray-900/50",
			"bg-yellow-100":
				"border-yellow-300 bg-yellow-50 dark:bg-yellow-950/30 dark:border-yellow-900/50",
			"bg-blue-100":
				"border-blue-300 bg-blue-50 dark:bg-blue-950/30 dark:border-blue-900/50",
			"bg-green-100":
				"border-green-300 bg-green-50 dark:bg-green-950/30 dark:border-green-900/50",
			"bg-red-100":
				"border-red-300 bg-red-50 dark:bg-red-950/30 dark:border-red-900/50",
			"bg-red-400":
				"border-red-600 bg-red-400/20 dark:bg-red-950/30 dark:border-red-900/50",
		};
		return (
			bgMap[bgColor ?? ""] ??
			"border-success-border bg-success-soft dark:bg-green-950/30 dark:border-green-900/50"
		);
	};

	return (
		<TooltipProvider>
			<Card className={`border-2 ${getBgColor()}`}>
				<CardHeader>
					<CardTitle>Active Deployment</CardTitle>
				</CardHeader>
				<CardContent className="space-y-6">
					{/* Deployment Info */}
					<div className="space-y-3">
						<div className="flex items-center justify-between">
							<span className="text-sm font-medium text-foreground">
								Status
							</span>
							<Tooltip>
								<TooltipTrigger>
									<Badge
										variant="default"
										className={`text-xs font-semibold ${
											BADGE_COLOR_MAP[deployment.status]
										}`}
									>
										{DeploymentPhase[deployment.status] || "unknown"}
									</Badge>
								</TooltipTrigger>
								<TooltipContent>{getPhaseTooltip(deployment)}</TooltipContent>
							</Tooltip>
						</div>

						<div className="flex items-center justify-between">
							<span className="text-sm font-medium text-foreground">
								Deployment ID
							</span>
							<Tooltip>
								<TooltipTrigger>
									<span className="text-sm font-mono text-foreground opacity-70 cursor-help">
										{formatShortId(deployment.id)}
									</span>
								</TooltipTrigger>
								<TooltipContent>
									{deployment.id}
								</TooltipContent>
							</Tooltip>
						</div>

						<div className="flex items-center justify-between">
							<span className="text-sm font-medium text-foreground">
								Created
							</span>
							<span className="text-sm text-foreground opacity-70">
								{formatTimestamp(deployment.createdAt)}
							</span>
						</div>

						{getImage(deployment) !== "—" && (
							<div className="flex items-start justify-between gap-2">
								<span className="text-sm font-medium text-foreground">
									Image
								</span>
								<span className="text-xs font-mono text-foreground opacity-70 text-right break-all max-w-xs">
									{getImage(deployment)}
								</span>
							</div>
						)}

						<div className="flex items-center justify-between">
							<span className="text-sm font-medium text-foreground">
								Region
							</span>
							<span className="text-sm font-mono text-foreground opacity-70">
								{deployment.region || "—"}
							</span>
						</div>

						<div className="flex gap-6">
							<div className="flex flex-col gap-1 flex-1">
								<span className="text-xs font-medium text-foreground opacity-70 h-4">
									Replicas
								</span>
								<span className="text-sm font-mono h-5">
									{deployment.replicas}
								</span>
							</div>
							<div className="flex flex-col gap-1 flex-1">
								<span className="text-xs font-medium text-foreground opacity-70 h-4">
									CPU
								</span>
								<span className="text-sm font-mono h-5">
									{cpuOptions[initialCpuIndex >= 0 ? initialCpuIndex : 3]}
								</span>
							</div>
							<div className="flex flex-col gap-1 flex-1">
								<span className="text-xs font-medium text-foreground opacity-70 h-4">
									Memory
								</span>
								<span className="text-sm font-mono h-5">
									{
										memoryOptions[
											initialMemoryIndex >= 0 ? initialMemoryIndex : 1
										]
									}
								</span>
							</div>
						</div>
					</div>

					{/* Scale Button */}
					{!isEditing && (
						<div className="flex justify-end">
							<Button
								variant="outline"
								size="sm"
								onClick={() => { setIsEditing(true); }}
							>
								Scale
							</Button>
						</div>
					)}

					{/* Scale Controls */}
					{isEditing && (
						<div className="space-y-6 border-t border-border dark:border-slate-700 pt-6">
							{/* Replicas, CPU & Memory */}
							<div className="flex gap-6">
								{/* Replicas */}
								<div className="space-y-3 pr-6 border-r border-border dark:border-slate-700">
									<Label className="text-sm font-medium block">Replicas</Label>
									<NumberInput
										value={replicas}
										onChange={setReplicas}
										min={1}
										max={10}
										disabled={scaleResourceMutation.isPending || isLoading}
										className="w-40"
									/>
								</div>

								{/* CPU */}
								<div className="space-y-3 flex-1 pr-6 border-r border-border dark:border-slate-700">
									<div className="flex items-center justify-between">
										<div className="flex items-center gap-2">
											<Cpu className="w-4 h-4 text-muted-foreground" />
											<Label className="text-sm font-medium">CPU</Label>
										</div>
										<span className="text-sm font-semibold text-foreground">
											{cpuOptions[cpuIndex]}
										</span>
									</div>
									<Slider
										value={[cpuIndex]}
										onValueChange={(value) => { setCpuIndex(value[0]); }}
										min={0}
										max={cpuOptions.length - 1}
										step={1}
										disabled={scaleResourceMutation.isPending || isLoading}
										className="w-full"
									/>
									<div className="flex justify-between text-xs text-muted-foreground">
										<span>100m</span>
										<span>2000m</span>
									</div>
								</div>

								{/* Memory */}
								<div className="space-y-3 flex-1">
									<div className="flex items-center justify-between">
										<div className="flex items-center gap-2">
											<HardDrive className="w-4 h-4 text-muted-foreground" />
											<Label className="text-sm font-medium">Memory</Label>
										</div>
										<span className="text-sm font-semibold text-foreground">
											{memoryOptions[memoryIndex]}
										</span>
									</div>
									<Slider
										value={[memoryIndex]}
										onValueChange={(value) => { setMemoryIndex(value[0]); }}
										min={0}
										max={memoryOptions.length - 1}
										step={1}
										disabled={scaleResourceMutation.isPending || isLoading}
										className="w-full"
									/>
									<div className="flex justify-between text-xs text-muted-foreground">
										<span>256Mi</span>
										<span>2Gi</span>
									</div>
								</div>
							</div>

							<div className="flex gap-2 pt-4 justify-end">
								<Button
									variant="outline"
									size="sm"
									onClick={() => { setIsEditing(false); }}
								>
									Cancel
								</Button>
								<Button
									size="sm"
									onClick={() => {
										void handleApply();
									}}
									disabled={!hasChanges || scaleResourceMutation.isPending}
								>
									{scaleResourceMutation.isPending ? "Applying..." : "Apply"}
								</Button>
							</div>
						</div>
					)}
				</CardContent>
			</Card>
		</TooltipProvider>
	);
}
