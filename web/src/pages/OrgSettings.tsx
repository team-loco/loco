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
import { getOrg } from "@/gen/org/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

export function OrgSettings() {
	const { orgId } = useParams<{ orgId: string }>();
	const [isEditing, setIsEditing] = useState(false);
	const [orgName, setOrgName] = useState("");

	const { data: orgRes, isLoading: orgLoading } = useQuery(
		getOrg,
		orgId ? { orgId: BigInt(orgId) } : undefined,
		{ enabled: !!orgId }
	);

	const org = orgRes?.org;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		orgId ? { orgId: BigInt(orgId) } : undefined,
		{ enabled: !!orgId }
	);
	const workspaces = workspacesRes?.workspaces ?? [];

	// Update orgName when org data loads
	if (org && !orgName && !isEditing) {
		setOrgName(org.name);
	}

	const handleSave = () => {
		toast.success("Organization updated");
		setIsEditing(false);
	};

	if (orgLoading || !org) {
		return <div>Loading...</div>;
	}

	return (
		<div className="max-w-4xl mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl font-bold text-foreground mb-2">
					Organization Settings
				</h1>
				<p className="text-muted-foreground">Manage {org.name}</p>
			</div>

			{/* Org Info */}
			<Card className="mb-6">
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
							<Button variant="neutral" onClick={() => setIsEditing(true)}>
								Edit Organization
							</Button>
						) : (
							<>
								<Button
									variant="neutral"
									onClick={() => {
										setIsEditing(false);
										setOrgName(org.name);
									}}
								>
									Cancel
								</Button>
								<Button onClick={handleSave}>Save Changes</Button>
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
									className="flex items-center justify-between p-4 border border-border rounded-neo"
								>
									<div>
										<p className="font-medium text-foreground">{ws.name}</p>
										<p className="text-xs text-muted-foreground mt-1">
											ID: {ws.id}
										</p>
									</div>
									<div className="flex gap-2">
										<Button
											variant="neutral"
											size="sm"
											onClick={() => console.log("Edit workspace", ws.id)}
										>
											Edit
										</Button>
										<Button
											variant="neutral"
											size="sm"
											className="text-red-600 border-red-200 hover:bg-red-50"
											onClick={() => console.log("Delete workspace", ws.id)}
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
							variant="neutral"
							onClick={() => console.log("Create workspace")}
							disabled
						>
							Create New Workspace
						</Button>
						<p className="text-xs text-muted-foreground mt-2">
							Workspace management coming in Phase 6
						</p>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
