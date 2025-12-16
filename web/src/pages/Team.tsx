import { useSearchParams } from "react-router";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { listMembers, listWorkspaces, removeMember } from "@/gen/workspace/v1";
import { getCurrentUser } from "@/gen/user/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useState, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getColumns } from "./team/columns";
import { DataTable } from "./team/data-table";

export function Team() {
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const queryClient = useQueryClient();
	const [page, setPage] = useState(0);
	const ITEMS_PER_PAGE = 10;

	const { data: userRes } = useQuery(getCurrentUser, {});
	const currentUser = userRes?.user;

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const orgs = useMemo(() => orgsRes?.orgs ?? [], [orgsRes]);
	const firstOrgId = orgs[0]?.id ?? null;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);
	const workspaces = useMemo(() => workspacesRes?.workspaces ?? [], [workspacesRes]);

	const firstWorkspaceId = useMemo(() => {
		if (workspaceFromUrl) return BigInt(workspaceFromUrl);
		if (workspaces.length > 0) {
			return workspaces[0].id;
		}
		return null;
	}, [workspaceFromUrl, workspaces]);

	const { data: membersRes, isLoading } = useQuery(
		listMembers,
		firstWorkspaceId
			? {
					workspaceId: firstWorkspaceId,
					limit: ITEMS_PER_PAGE,
					offset: page * ITEMS_PER_PAGE,
			  }
			: undefined,
		{ enabled: !!firstWorkspaceId }
	);
	const members = membersRes?.members ?? [];
	const totalMembers = membersRes?.total ?? 0;
	const totalPages = Math.ceil(totalMembers / ITEMS_PER_PAGE);

	// TODO: Get user info for each member (name, email, avatar)
	const isAdmin = currentUser?.role === "admin" || currentUser?.role === "workspace_admin";

	const { mutate: removeMemberMutation, isPending: isRemoving } = useMutation(removeMember, {
		onSuccess: () => {
			if (firstWorkspaceId) {
				queryClient.invalidateQueries({
					queryKey: [{ service: "loco.workspace.v1.WorkspaceService", method: "ListMembers" }],
				});
			}
		},
	});

	const handleRemoveMember = async (userId: bigint) => {
		if (!firstWorkspaceId) return;
		try {
			await removeMemberMutation({
				workspaceId: firstWorkspaceId,
				userId,
			});
		} catch (error) {
			console.error("Failed to remove member:", error);
		}
	};

	const columns = useMemo(
		() => getColumns(isAdmin, handleRemoveMember, isRemoving),
		[isAdmin, isRemoving]
	);

	return (
		<div className="space-y-6">
			<div>
				<h1 className="text-3xl font-bold tracking-tight">Team</h1>
				<p className="text-muted-foreground mt-2">
					Manage workspace members and their permissions
				</p>
			</div>

			<Card>
				<CardHeader>
					<CardTitle>Workspace Members</CardTitle>
					<CardDescription>
						{totalMembers} total member{totalMembers !== 1 ? "s" : ""} in this workspace
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-6">
					<DataTable columns={columns} data={members} isLoading={isLoading} />

					<div className="flex items-center justify-between">
						<div className="text-sm text-muted-foreground">
							Page {page + 1} of {totalPages || 1} • Showing {members.length} of {totalMembers}
						</div>
						<div className="flex gap-2">
							<Button
								variant="outline"
								size="sm"
								onClick={() => setPage((p) => Math.max(0, p - 1))}
								disabled={page === 0 || isLoading}
							>
								Previous
							</Button>
							<Button
								variant="outline"
								size="sm"
								onClick={() => setPage((p) => p + 1)}
								disabled={page >= totalPages - 1 || isLoading}
							>
								Next
							</Button>
						</div>
					</div>

					<div className="flex justify-center">
						<Button variant="default">
							<span>Invite user</span>
						</Button>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
