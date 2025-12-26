import { useState } from "react";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Slider } from "@/components/ui/slider";
import { scaleResource } from "@/gen/resource/v1";
import { useMutation } from "@connectrpc/connect-query";
import { Loader2, Cpu, HardDrive, Layers } from "lucide-react";

interface ScaleCardProps {
	appId: string;
	currentReplicas?: number;
	isLoading?: boolean;
}

export function ScaleCard({
	appId,
	currentReplicas = 1,
	isLoading = false,
}: ScaleCardProps) {
	const [replicas, setReplicas] = useState<number>(currentReplicas);
	const [cpuIndex, setCpuIndex] = useState<number>(3); // 1000m by default
	const [memoryIndex, setMemoryIndex] = useState<number>(1); // 512Mi by default

	const cpuOptions = [
		"250m",
		"500m",
		"750m",
		"1000m",
		"1250m",
		"1500m",
		"1750m",
		"2000m",
	];
	const memoryOptions = [
		"256Mi",
		"512Mi",
		"768Mi",
		"1Gi",
		"1.25Gi",
		"1.5Gi",
		"2Gi",
	];

	const { mutate: scale, isPending } = useMutation(scaleResource);

	const handleScale = async () => {
		try {
			await scale({
				resourceId: BigInt(appId),
				replicas,
				cpu: cpuOptions[cpuIndex],
				memory: memoryOptions[memoryIndex],
			});
		} catch (error) {
			console.error("Failed to scale resource:", error);
		}
	};

	const handleReplicasChange = (value: string) => {
		const num = parseInt(value, 10);
		if (!isNaN(num) && num > 0) {
			setReplicas(num);
		}
	};

	return (
		<Card>
			<CardHeader>
				<CardTitle>Scale Resource</CardTitle>
				<CardDescription>Adjust replicas, CPU, and memory</CardDescription>
			</CardHeader>
			<CardContent className="space-y-6">
				{/* Replicas with CPU & Memory */}
				<div className="flex gap-6">
					{/* Replicas */}
					<div className="space-y-3 flex-1">
						<div className="flex items-center justify-between">
							<div className="flex items-center gap-2">
								<Layers className="w-4 h-4 text-muted-foreground" />
								<Label className="text-sm font-medium">Replicas</Label>
							</div>
							<span className="text-sm font-semibold text-foreground">
								{replicas}
							</span>
						</div>
						<div className="flex items-center gap-3">
							<Input
								type="number"
								min="1"
								value={replicas}
								onChange={(e) => handleReplicasChange(e.target.value)}
								disabled={isPending || isLoading}
								className="w-20 text-center"
							/>
							<span className="text-xs text-muted-foreground">instances</span>
						</div>
					</div>

					<div className="border-r border-border"></div>

					{/* CPU & Memory */}
					<div className="flex gap-6 flex-[2]">
						{/* CPU */}
						<div className="space-y-3 flex-1 pr-6">
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
								onValueChange={(value) => setCpuIndex(value[0])}
								min={0}
								max={cpuOptions.length - 1}
								step={1}
								disabled={isPending || isLoading}
								className="w-full"
							/>
							<div className="flex justify-between text-xs text-muted-foreground">
								<span>250m</span>
								<span>2000m</span>
							</div>
						</div>

						{/* Memory */}
						<div className="space-y-3 flex-1 pl-6 border-l border-border">
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
								onValueChange={(value) => setMemoryIndex(value[0])}
								min={0}
								max={memoryOptions.length - 1}
								step={1}
								disabled={isPending || isLoading}
								className="w-full"
							/>
							<div className="flex justify-between text-xs text-muted-foreground">
								<span>256Mi</span>
								<span>2Gi</span>
							</div>
						</div>
					</div>
				</div>

				{/* Action Button */}
				<Button
					onClick={handleScale}
					disabled={isPending || isLoading}
					className="w-full"
				>
					{isPending || isLoading ? (
						<>
							<Loader2 className="mr-2 h-4 w-4 animate-spin" />
							Scaling...
						</>
					) : (
						"Apply Scaling"
					)}
				</Button>
			</CardContent>
		</Card>
	);
}
