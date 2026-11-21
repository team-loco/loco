import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { EntityType, Scope } from "@gen/loco/token/v1/token_pb";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";

const ADMIN = scopeLevel(Scope.ADMIN);

/**
 * Whose audit log the caller reads in an organization: its admins read all of it, workspace
 * admins only the workspaces they administer (`workspaces`), everyone else none of it.
 */
export function useAuditAccess(orgId: string | null) {
	const transport = useTransport();
	const scopes = useMyScopes();
	const org = orgId !== null && scopes.orgLevel(orgId) >= ADMIN;
	const adminScopes =
		orgId === null || org || !scopes.orgMember(orgId)
			? []
			: scopes.scopes.filter((s) => s.entityType === EntityType.WORKSPACE && scopeLevel(s.scope) >= ADMIN);
	const lookups = useQueries({
		queries: [...new Set(adminScopes.map((s) => s.entityId))].map((workspaceId) =>
			createQueryOptions(getWorkspace, { workspaceId }, { transport }),
		),
	});
	const administered = lookups.map((q) => q.data?.workspace).filter((w): w is Workspace => w?.orgId === orgId);
	return {
		isLoading: scopes.isLoading || lookups.some((q) => q.isPending),
		org,
		workspaces: administered,
		visible: org || administered.length > 0,
		reads: (workspaceId: string) => org || administered.some((w) => w.id === workspaceId),
	};
}
