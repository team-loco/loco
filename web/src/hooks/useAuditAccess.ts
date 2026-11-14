import { Scope } from "@gen/loco/token/v1/token_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";

const ADMIN = scopeLevel(Scope.ADMIN);

/**
 * Whose audit log the caller reads in an organization: its admins read all of it, workspace
 * admins only the workspaces they administer (`workspaces`), everyone else none of it.
 */
export function useAuditAccess(orgId: string | null) {
	const scopes = useMyScopes();
	const { workspaces } = useOrgWorkspace();
	const org = orgId !== null && scopes.orgLevel(orgId) >= ADMIN;
	const administered: Workspace[] =
		orgId === null ? [] : workspaces.filter((w) => w.orgId === orgId && scopes.workspaceLevel(orgId, w.id) >= ADMIN);
	return { isLoading: scopes.isLoading, org, workspaces: administered, visible: org || administered.length > 0 };
}
