import { Code, ConnectError } from "@connectrpc/connect";
import { EntityType, type EntityScope } from "@gen/loco/token/v1/token_pb";

import { hasOrganizationMembership } from "@/hooks/useMyScopes";

export function resourceMissing(error: unknown, canReadWorkspace: boolean): boolean {
	if (!(error instanceof ConnectError)) return false;
	if (error.code === Code.NotFound) return true;
	return error.code === Code.PermissionDenied && canReadWorkspace;
}

export function workspaceKnown(
	orgId: string,
	workspaceId: string,
	workspaces: readonly { id: string }[],
	scopes: readonly EntityScope[],
): boolean {
	return (
		hasOrganizationMembership(scopes, orgId) ||
		workspaces.some((w) => w.id === workspaceId) ||
		scopes.some(
			(s) =>
				s.entityType === EntityType.RESOURCE || (s.entityType === EntityType.WORKSPACE && s.entityId === workspaceId),
		)
	);
}
