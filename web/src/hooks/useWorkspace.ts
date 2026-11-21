import { useQuery } from "@connectrpc/connect-query";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { useMyScopes } from "@/hooks/useMyScopes";

/** One workspace by ID, for a caller whose scopes read it. */
export function useWorkspace(orgId: string | null, workspaceId: string | null): Workspace | undefined {
	const scopes = useMyScopes();
	const enabled =
		orgId !== null && workspaceId !== null && !scopes.isLoading && scopes.workspaceLevel(orgId, workspaceId) >= 1;
	return useQuery(getWorkspace, { workspaceId: workspaceId ?? "" }, { enabled }).data?.workspace;
}
