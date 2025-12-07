import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { useMutation } from "@connectrpc/connect-query";
import { updateAppEnv } from "@/gen/app/v1";
import { useState } from "react";
import { Trash2, Plus } from "lucide-react";

interface EnvVar {
	key: string;
	value: string;
}

interface EnvironmentVariablesProps {
	appId: string;
	envVars?: EnvVar[];
	isLoading?: boolean;
}

export function EnvironmentVariables({
	appId,
	envVars = [],
	isLoading = false,
}: EnvironmentVariablesProps) {
	const [isEditing, setIsEditing] = useState(false);
	const [vars, setVars] = useState<EnvVar[]>(envVars);
	const updateEnvMutation = useMutation(updateAppEnv);

	const handleAdd = () => {
		setVars([...vars, { key: "", value: "" }]);
	};

	const handleRemove = (index: number) => {
		setVars(vars.filter((_, i) => i !== index));
	};

	const handleChange = (index: number, field: "key" | "value", value: string) => {
		const newVars = [...vars];
		newVars[index] = { ...newVars[index], [field]: value };
		setVars(newVars);
	};

	const handleSave = async () => {
		try {
			// Filter out empty entries
			const cleanedVars = vars.filter((v) => v.key.trim());
			const envMap = cleanedVars.reduce(
				(acc, v) => {
					acc[v.key] = v.value;
					return acc;
				},
				{} as { [key: string]: string }
			);
			await updateEnvMutation.mutateAsync({
				appId: BigInt(appId),
				env: envMap,
			});
			setIsEditing(false);
		} catch (error) {
			console.error("Failed to update env vars:", error);
		}
	};

	const hasChanges = JSON.stringify(vars) !== JSON.stringify(envVars);

	if (isLoading) {
		return (
			<Card className="animate-pulse">
				<CardContent className="p-6">
					<div className="h-6 bg-main/20 rounded w-1/4"></div>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle className="flex items-center justify-between">
					<span>Environment Variables ({vars.length})</span>
					{!isEditing && (
						<Button
							variant="outline"
							size="sm"
							onClick={() => setIsEditing(true)}
							className="border-2"
						>
							Edit
						</Button>
					)}
				</CardTitle>
			</CardHeader>
			<CardContent>
				{isEditing ? (
					<div className="space-y-4">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>Key</TableHead>
									<TableHead>Value</TableHead>
									<TableHead className="text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{vars.map((envVar, index) => (
									<TableRow key={index}>
										<TableCell>
											<Input
												value={envVar.key}
												onChange={(e) =>
													handleChange(
														index,
														"key",
														e.target.value
													)
												}
												placeholder="KEY"
												className="font-mono text-sm"
											/>
										</TableCell>
										<TableCell>
											<Input
												type="password"
												value={envVar.value}
												onChange={(e) =>
													handleChange(
														index,
														"value",
														e.target.value
													)
												}
												placeholder="value"
												className="font-mono text-sm"
											/>
										</TableCell>
										<TableCell className="text-right">
											<Button
												variant="secondary"
												size="sm"
												onClick={() => handleRemove(index)}
												className="h-8 w-8 p-0"
											>
												<Trash2 className="w-4 h-4" />
											</Button>
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>

						<Button
							variant="outline"
							size="sm"
							onClick={handleAdd}
							className="border-2 w-full"
						>
							<Plus className="w-4 h-4 mr-2" />
							Add Variable
						</Button>

						<div className="flex gap-2 pt-4">
							<Button
								variant="outline"
								onClick={() => {
									setVars(envVars);
									setIsEditing(false);
								}}
								className="flex-1"
							>
								Cancel
							</Button>
							<Button
								onClick={handleSave}
								disabled={!hasChanges || updateEnvMutation.isPending}
								className="flex-1"
							>
								{updateEnvMutation.isPending ? "Saving..." : "Save"}
							</Button>
						</div>
					</div>
				) : vars.length > 0 ? (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Key</TableHead>
								<TableHead>Value</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{vars.map((envVar, index) => (
								<TableRow key={index}>
									<TableCell className="font-mono text-sm">
										{envVar.key}
									</TableCell>
									<TableCell className="font-mono text-sm">
										{envVar.value
											? "•".repeat(
													Math.min(
														envVar.value.length,
														8
													)
												)
											: "—"}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				) : (
					<p className="text-sm text-foreground opacity-60">
						No environment variables set
					</p>
				)}
			</CardContent>
		</Card>
	);
}
