import type { Organization } from "@gen/loco/org/v1/org_pb";
import { EntityType, type EntityScope } from "@gen/loco/token/v1/token_pb";

export interface AccountBlockers {
	orgs: Organization[];
	workspaceIds: string[];
}

export function accountBlockers(
	userId: string,
	orgs: readonly Organization[],
	scopes: readonly EntityScope[],
): AccountBlockers {
	const workspaceIds = new Set(scopes.filter((s) => s.entityType === EntityType.WORKSPACE).map((s) => s.entityId));
	return {
		orgs: orgs.filter((o) => o.createdBy === userId),
		workspaceIds: [...workspaceIds],
	};
}
