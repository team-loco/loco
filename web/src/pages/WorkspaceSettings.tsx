import { useParams } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { getWorkspace } from "@/gen/workspace/v1";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useState } from "react";
import { toast } from "sonner";

export function WorkspaceSettings() {
	const { workspaceId } = useParams<{ workspaceId: string }>();
	const [isEditing, setIsEditing] = useState(false);
	const [wsName, setWsName] = useState("");
	const [wsDescription, setWsDescription] = useState("");

	const { data: wsRes, isLoading: wsLoading } = useQuery(
		getWorkspace,
		workspaceId ? { id: BigInt(workspaceId) } : undefined,
		{ enabled: !!workspaceId }
	);

	const workspace = wsRes?.workspace;

	// Update state when workspace data loads
	if (workspace && !wsName && !isEditing) {
		setWsName(workspace.name);
		setWsDescription(workspace.description ?? "");
	}

	const handleSave = () => {
		toast.success("Workspace updated");
		setIsEditing(false);
	};

	const handleDelete = () => {
		if (confirm("Are you sure? This action cannot be undone.")) {
			toast.success("Workspace deleted");
		}
	};

	if (wsLoading || !workspace) {
		return <div>Loading...</div>;
	}

	return (
		<div className="max-w-4xl mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl font-heading font-bold text-foreground mb-2">
					Workspace Settings
				</h1>
				<p className="text-muted-foreground">
					Manage {workspace.name}
				</p>
			</div>

			{/* Workspace Info */}
			<Card className="mb-6">
				<CardHeader>
					<CardTitle className="text-lg">Workspace Information</CardTitle>
				</CardHeader>
				<CardContent className="space-y-6">
					{/* Workspace Name */}
					<div>
						<Label htmlFor="ws-name" className="text-sm mb-2 block">
							Workspace Name
						</Label>
						<Input
							id="ws-name"
							value={isEditing ? wsName : workspace.name}
							onChange={(e) => setWsName(e.target.value)}
							disabled={!isEditing}
							className="border-border"
						/>
					</div>

					{/* Description */}
					<div>
						<Label htmlFor="ws-desc" className="text-sm mb-2 block">
							Description
						</Label>
						<Textarea
							id="ws-desc"
							value={isEditing ? wsDescription : (workspace.description ?? "")}
							onChange={(e) => setWsDescription(e.target.value)}
							disabled={!isEditing}
							className="border-border"
							placeholder="Describe this workspace..."
							rows={3}
						/>
					</div>

					{/* Actions */}
					<div className="flex gap-3 pt-4 border-t border-border">
						{!isEditing ? (
							<Button
								variant="neutral"
								onClick={() => setIsEditing(true)}
							>
								Edit Workspace
							</Button>
						) : (
							<>
								<Button
									variant="neutral"
									onClick={() => {
										setIsEditing(false);
										setWsName(workspace.name);
										setWsDescription(workspace.description ?? "");
									}}
								>
									Cancel
								</Button>
								<Button onClick={handleSave}>
									Save Changes
								</Button>
							</>
						)}
					</div>
				</CardContent>
			</Card>

			{/* Members */}
			<Card className="mb-6">
				<CardHeader>
					<CardTitle className="text-lg">Members</CardTitle>
					<CardDescription>
						Manage who has access to this workspace
					</CardDescription>
				</CardHeader>
				<CardContent>
					<div className="space-y-4">
						<div className="text-sm text-muted-foreground italic">
							Member management coming in Phase 6
						</div>
						<Button variant="neutral" disabled>
							Add Member
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Danger Zone */}
			<Card className="border-red-200 bg-red-50/50">
				<CardHeader>
					<CardTitle className="text-lg text-red-700">Danger Zone</CardTitle>
					<CardDescription>
						Irreversible actions
					</CardDescription>
				</CardHeader>
				<CardContent>
					<Button
						variant="neutral"
						className="text-red-600 border-red-200 hover:bg-red-50"
						onClick={handleDelete}
					>
						Delete Workspace
					</Button>
					<p className="text-xs text-muted-foreground mt-3">
						This will permanently delete this workspace and all its apps.
					</p>
				</CardContent>
			</Card>
		</div>
	);
}
