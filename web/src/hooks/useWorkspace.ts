import { useQuery } from "@connectrpc/connect-query";
import { EntityType } from "@gen/loco/token/v1/token_pb";
import { getWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { useMyScopes } from "@/hooks/useMyScopes";
import { refetchUnlessMissing } from "@/lib/not-found";

/** How long a looked-up workspace counts as fresh, so the shell's readers share one request. */
export const WORKSPACE_STALE_MS = 60_000;
// The most ListOrgWorkspaces and ListOrgResources return in one page, and the most IDs they filter by.
export const MAX_PAGE = 200;

/**
 * One workspace by ID. A caller who reads it gets it from GetWorkspace. An organization member who
 * reaches it only through resource grants sees just the workspaces holding those resources, so
 * one page of ListOrgWorkspaces holds it.
 */
export function useWorkspace(orgId: string | null, workspaceId: string | null) {
	const scopes = useMyScopes();
	const known = orgId !== null && workspaceId !== null && !scopes.isLoading;
	const reads = known && scopes.workspaceLevel(orgId, workspaceId) >= 1;
	const viaResources =
		known && !reads && scopes.orgMember(orgId) && scopes.scopes.some((s) => s.entityType === EntityType.RESOURCE);
	const direct = useQuery(
		getWorkspace,
		{ workspaceId: workspaceId ?? "" },
		{
			enabled: reads,
			staleTime: WORKSPACE_STALE_MS,
			retryOnMount: false,
			refetchOnWindowFocus: refetchUnlessMissing(true),
		},
	);
	const granted = useQuery(
		listOrgWorkspaces,
		{ orgId: orgId ?? "", pageSize: MAX_PAGE },
		{ enabled: viaResources, staleTime: WORKSPACE_STALE_MS },
	);
	const workspace: Workspace | undefined =
		direct.data?.workspace ?? granted.data?.workspaces.find((w) => w.id === workspaceId);
	// A disabled query reports another reader's fetch of the same workspace, so only enabled ones count.
	return {
		workspace,
		isLoading: scopes.isLoading || (reads && direct.isLoading) || (viaResources && granted.isLoading),
	};
}
