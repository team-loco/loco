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
import { getOrg, updateOrg } from "@/gen/org/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
import { getErrorMessage } from "@/lib/error-handler";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import Loader from "@/assets/loader.svg?react";
import { CreateWorkspaceDialog } from "@/components/workspace/CreateWorkspaceDialog";
import { DeleteWorkspaceDialog } from "@/components/workspace/DeleteWorkspaceDialog";
import { DeleteOrgDialog } from "@/components/org/DeleteOrgDialog";

export function OrgSettings() {
	const { orgId } = useParams<{ orgId: string }>();
	const navigate = useNavigate();
	const [isEditing, setIsEditing] = useState(false);
	const [orgName, setOrgName] = useState("");
	const [createWorkspaceOpen, setCreateWorkspaceOpen] = useState(false);
	const [deleteOrgOpen, setDeleteOrgOpen] = useState(false);
	const [deleteWorkspaceId, setDeleteWorkspaceId] = useState<bigint | null>(
		null
	);
	const [deleteWorkspaceName, setDeleteWorkspaceName] = useState("");

	const {
		data: orgResponse,
		isLoading: orgLoading,
		refetch,
	} = useQuery(
		getOrg,
		orgId ? { key: { case: "orgId", value: BigInt(orgId) } } : undefined,
		{ enabled: !!orgId }
	);

	const org = orgResponse?.organization;

	const { data: workspacesRes, refetch: refetchWorkspaces } = useQuery(
		listOrgWorkspaces,
		orgId ? { orgId: BigInt(orgId) } : undefined,
		{ enabled: !!orgId }
	);
	const workspaces = workspacesRes?.workspaces ?? [];

	const { mutate: mutateUpdateOrg, isPending: isUpdatePending } =
		useMutation(updateOrg);

	// Update orgName when org data loads
	if (org && !orgName && !isEditing) {
		setOrgName(org.name);
	}

	const handleSave = () => {
		if (!orgId) return;
		mutateUpdateOrg(
			{
				orgId: BigInt(orgId),
				name: orgName,
			},
			{
				onSuccess: () => {
					refetch();
					toast.success("Organization updated");
					setIsEditing(false);
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to update organization"));
				},
			}
		);
	};

	if (orgLoading || !org) {
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
			<div className="space-y-6">
				{/* Org Info */}
				<Card>
				<CardHeader>
					<CardTitle className="text-lg">Organization Information</CardTitle>
				</CardHeader>
				<CardContent className="space-y-6">
					{/* Org Name */}
					<div>
						<Label htmlFor="org-name" className="text-sm mb-2 block">
							Organization Name
						</Label>
						<Input
							id="org-name"
							value={isEditing ? orgName : org.name}
							onChange={(e) => setOrgName(e.target.value)}
							disabled={!isEditing}
							className="border-border"
						/>
					</div>

					{/* Created Date */}
					<div>
						<Label className="text-sm mb-2 block">Created</Label>
						<p className="text-sm text-muted-foreground">
							{new Date(
								org.createdAt?.seconds
									? Number(org.createdAt.seconds) * 1000
									: 0
							).toLocaleDateString()}
						</p>
					</div>

					{/* Member Count */}
					<div>
						<Label className="text-sm mb-2 block">Members</Label>
						<p className="text-sm text-muted-foreground">
							View and manage members from workspace settings
						</p>
					</div>

					{/* Actions */}
					<div className="flex gap-3 pt-4 border-t border-border">
						{!isEditing ? (
							<Button variant="secondary" onClick={() => setIsEditing(true)}>
								Edit Organization
							</Button>
						) : (
							<>
								<Button
									variant="secondary"
									onClick={() => {
										setIsEditing(false);
										setOrgName(org.name);
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

				{/* Workspaces */}
				<Card>
				<CardHeader>
					<CardTitle className="text-lg">Workspaces</CardTitle>
					<CardDescription>
						Manage workspaces within this organization
					</CardDescription>
				</CardHeader>
				<CardContent>
					{workspaces.length === 0 ? (
						<p className="text-sm text-muted-foreground">No workspaces yet</p>
					) : (
						<div className="space-y-3">
							{workspaces.map((ws) => (
								<div
									key={ws.id}
									className="flex items-center justify-between p-3 border border-border rounded-lg"
								>
									<div>
										<p className="font-medium text-foreground">{ws.name}</p>
									</div>
									<div className="flex gap-2">
										<Button
											variant="secondary"
											size="sm"
											onClick={() => navigate(`/workspace/${ws.id}/settings`)}
										>
											Edit
										</Button>
										<Button
											variant="destructive"
											size="sm"
											onClick={() => {
												setDeleteWorkspaceId(ws.id);
												setDeleteWorkspaceName(ws.name);
											}}
										>
											Delete
										</Button>
									</div>
								</div>
							))}
						</div>
					)}

					<div className="mt-4 pt-4 border-t border-border">
						<Button
							variant="secondary"
							onClick={() => setCreateWorkspaceOpen(true)}
						>
							Create New Workspace
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
					<Button variant="destructive" onClick={() => setDeleteOrgOpen(true)}>
						Delete Organization
					</Button>
					<p className="text-xs text-muted-foreground mt-3">
						This will permanently delete this organization, all workspaces, and
						all resources.
					</p>
				</CardContent>
				</Card>
			</div>

			{/* Dialogs */}
			{orgId && org && (
				<>
					<CreateWorkspaceDialog
						open={createWorkspaceOpen}
						onOpenChange={setCreateWorkspaceOpen}
						orgId={BigInt(orgId)}
						onSuccess={() => {
							refetchWorkspaces();
						}}
					/>
					{deleteWorkspaceId && (
						<DeleteWorkspaceDialog
							open={deleteWorkspaceId !== null}
							onOpenChange={(open) => {
								if (!open) {
									setDeleteWorkspaceId(null);
									setDeleteWorkspaceName("");
								}
							}}
							workspaceId={deleteWorkspaceId}
							workspaceName={deleteWorkspaceName}
							onSuccess={() => {
								refetchWorkspaces();
							}}
						/>
					)}
					<DeleteOrgDialog
						open={deleteOrgOpen}
						onOpenChange={setDeleteOrgOpen}
						orgId={BigInt(orgId)}
						orgName={org.name}
					/>
				</>
			)}
		</div>
	);
}
