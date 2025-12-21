import { useState } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Slider } from "@/components/ui/slider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { scaleResource } from "@/gen/resource/v1";
import { useMutation } from "@connectrpc/connect-query";
import { Loader2 } from "lucide-react";

interface ScaleCardProps {
	appId: string;
	currentReplicas?: number;
	isLoading?: boolean;
}

export function ScaleCard({ appId, currentReplicas = 1, isLoading = false }: ScaleCardProps) {
	const [replicas, setReplicas] = useState<number>(currentReplicas);
	const [cpu, setCpu] = useState<string>("250m");
	const [memory, setMemory] = useState<string>("512Mi");

	const { mutate: scale, isPending } = useMutation(scaleResource);

	const handleScale = async () => {
		try {
			await scale({
				resourceId: BigInt(appId),
				replicas,
				cpu,
				memory,
			});
		} catch (error) {
			console.error("Failed to scale resource:", error);
		}
	};

	return (
		<Card>
			<CardHeader>
				<CardTitle>Scale Resource</CardTitle>
				<CardDescription>Adjust replicas and resource allocation</CardDescription>
			</CardHeader>
			<CardContent className="space-y-6">
				{/* Replicas */}
				<div className="space-y-3">
					<div className="flex items-center justify-between">
						<Label className="text-sm font-medium">Replicas</Label>
						<span className="text-sm font-semibold text-foreground">{replicas}</span>
					</div>
					<Slider
						value={[replicas]}
						onValueChange={(value) => setReplicas(value[0])}
						min={1}
						max={10}
						step={1}
						disabled={isPending || isLoading}
						className="w-full"
					/>
					<p className="text-xs text-muted-foreground">
						Number of running instances (1-10)
					</p>
				</div>

				{/* CPU */}
				<div className="space-y-2">
					<Label htmlFor="cpu" className="text-sm font-medium">
						CPU
					</Label>
					<Input
						id="cpu"
						placeholder="e.g., 250m, 500m, 1000m"
						value={cpu}
						onChange={(e) => setCpu(e.target.value)}
						disabled={isPending || isLoading}
						className="text-sm"
					/>
					<p className="text-xs text-muted-foreground">
						CPU request in millicores (250m, 500m, 1000m, etc.)
					</p>
				</div>

				{/* Memory */}
				<div className="space-y-2">
					<Label htmlFor="memory" className="text-sm font-medium">
						Memory
					</Label>
					<Input
						id="memory"
						placeholder="e.g., 256Mi, 512Mi, 1Gi"
						value={memory}
						onChange={(e) => setMemory(e.target.value)}
						disabled={isPending || isLoading}
						className="text-sm"
					/>
					<p className="text-xs text-muted-foreground">
						Memory request (256Mi, 512Mi, 1Gi, etc.)
					</p>
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
