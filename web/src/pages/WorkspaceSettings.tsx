import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { getWorkspace, updateWorkspace } from "@/gen/loco/workspace/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";
import Loader from "@/assets/loader.svg?react";
import { DeleteWorkspaceDialog } from "@/components/workspace/DeleteWorkspaceDialog";
import { getErrorMessage } from "@/lib/error-handler";

export function WorkspaceSettings() {
	const { workspaceId } = useParams<{ workspaceId: string }>();
	const [isEditing, setIsEditing] = useState(false);
	const [wsName, setWsName] = useState("");
	const [wsDescription, setWsDescription] = useState("");
	const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);

	const {
		data: workspaceResponse,
		isLoading: wsLoading,
		refetch,
	} = useQuery(
		getWorkspace,
		workspaceId ? { workspaceId: BigInt(workspaceId) } : undefined,
		{ enabled: !!workspaceId }
	);
	const workspace = workspaceResponse?.workspace;

	const { mutate: mutateUpdateWorkspace, isPending: isUpdatePending } =
		useMutation(updateWorkspace);

	// Update state when workspace data loads
	if (workspace && !wsName && !isEditing) {
		setWsName(workspace.name);
		setWsDescription(workspace.description ?? "");
	}

	const handleSave = () => {
		if (!workspaceId) return;

		mutateUpdateWorkspace(
			{
				workspaceId: BigInt(workspaceId),
				name: wsName.trim(),
				description: wsDescription.trim() || undefined,
			},
			{
				onSuccess: () => {
					refetch();
					toast.success("Workspace updated");
					setIsEditing(false);
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to update workspace"));
				},
			}
		);
	};

	if (wsLoading || !workspace) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center flex flex-col gap-2 items-center">
					<Loader className="w-8 h-8" />
					<p className="text-foreground font-base">Loading...</p>
				</div>
			</div>
		);
	}

	return (
		<div className="max-w-4xl mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl font-heading text-foreground mb-2">
					Workspace Settings
				</h1>
				<p className="text-muted-foreground">Manage {workspace.name}</p>
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
							value={isEditing ? wsDescription : workspace.description ?? ""}
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
							<Button variant="secondary" onClick={() => setIsEditing(true)}>
								Edit Workspace
							</Button>
						) : (
							<>
								<Button
									variant="secondary"
									onClick={() => {
										setIsEditing(false);
										setWsName(workspace.name);
										setWsDescription(workspace.description ?? "");
									}}
									disabled={isUpdatePending}
								>
									Cancel
								</Button>
								<Button onClick={handleSave} disabled={isUpdatePending}>
									{isUpdatePending ? (
										<>
											<Loader className="w-4 h-4 mr-2" />
											Saving...
										</>
									) : (
										"Save Changes"
									)}
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
						<Button variant="secondary" disabled>
							Add Member
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Danger Zone */}
			<Card className="border-red-200 bg-red-50/50 dark:border-red-900/50 dark:bg-red-900/10">
				<CardHeader>
					<CardTitle className="text-lg text-red-700 dark:text-red-500">
						Danger Zone
					</CardTitle>
					<CardDescription>Irreversible actions</CardDescription>
				</CardHeader>
				<CardContent>
					<Button
						variant="destructive"
						onClick={() => setDeleteDialogOpen(true)}
					>
						Delete Workspace
					</Button>
					<p className="text-xs text-muted-foreground mt-3">
						This will permanently delete this workspace and all its resources.
					</p>
				</CardContent>
			</Card>

			{/* Delete Dialog */}
			{workspaceId && workspace && (
				<DeleteWorkspaceDialog
					open={deleteDialogOpen}
					onOpenChange={setDeleteDialogOpen}
					workspaceId={BigInt(workspaceId)}
					workspaceName={workspace.name}
				/>
			)}
		</div>
	);
}
