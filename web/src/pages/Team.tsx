import { useSearchParams } from "react-router";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { listMembers, listWorkspaces, removeMember } from "@/gen/workspace/v1";
import { getCurrentUser } from "@/gen/user/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { toastConnectError } from "@/lib/error-handler";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useState, useMemo, useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getColumns } from "./team/columns";
import { DataTable } from "./team/data-table";

export function Team() {
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const queryClient = useQueryClient();
	const [cursors, setCursors] = useState<Array<bigint | null>>([null]);
	const [currentPage, setCurrentPage] = useState(0);
	const ITEMS_PER_PAGE = 10;

	useQuery(getCurrentUser, {});

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const orgs = useMemo(() => orgsRes?.orgs ?? [], [orgsRes]);
	const firstOrgId = orgs[0]?.id ?? null;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);
	const workspaces = useMemo(
		() => workspacesRes?.workspaces ?? [],
		[workspacesRes]
	);

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
					afterCursor: cursors[currentPage] ?? undefined,
			  }
			: undefined,
		{ enabled: !!firstWorkspaceId }
	);
	const members = membersRes?.members ?? [];
	const nextCursor = membersRes?.nextCursor ?? null;
	const hasNextPage = nextCursor !== null;

	// todo: fix admin checks after tvm
	const isAdmin = false;

	const { mutate: removeMemberMutation, isPending: isRemoving } = useMutation(
		removeMember,
		{
			onSuccess: () => {
				if (firstWorkspaceId) {
					queryClient.invalidateQueries({
						queryKey: [
							{
								service: "loco.workspace.v1.WorkspaceService",
								method: "ListMembers",
							},
						],
					});
				}
			},
		}
	);

	const handleRemoveMember = useCallback(
		async (userId: bigint) => {
			if (!firstWorkspaceId) return;
			try {
				removeMemberMutation({
					workspaceId: firstWorkspaceId,
					userId,
				});
			} catch (error) {
				toastConnectError(error, "Failed to remove member");
			}
		},
		[firstWorkspaceId, removeMemberMutation]
	);

	const columns = useMemo(
		() => getColumns(isAdmin, handleRemoveMember, isRemoving),
		[isAdmin, handleRemoveMember, isRemoving]
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
					<CardDescription>Manage members in this workspace</CardDescription>
				</CardHeader>
				<CardContent className="space-y-6">
					<DataTable columns={columns} data={members} isLoading={isLoading} />

					<div className="flex items-center justify-between">
						<div className="text-sm text-muted-foreground">
							Showing {members.length} member{members.length !== 1 ? "s" : ""}
						</div>
						<div className="flex gap-2">
							<Button
								variant="outline"
								size="sm"
								onClick={() => setCurrentPage((p) => Math.max(0, p - 1))}
								disabled={currentPage === 0 || isLoading}
							>
								Previous
							</Button>
							<Button
								variant="outline"
								size="sm"
								onClick={() => {
									if (hasNextPage) {
										setCursors((prev) => [...prev, nextCursor]);
										setCurrentPage((p) => p + 1);
									}
								}}
								disabled={!hasNextPage || isLoading}
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
