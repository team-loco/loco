import { useQuery } from "@connectrpc/connect-query";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType, Scope, type EntityScope } from "@gen/loco/token/v1/token_pb";

export type AccessLevel = 0 | 1 | 2 | 3;

/** "workspace" for a caller who reads the whole workspace, else the IDs of the resources its grants name. */
export type ResourceGrants = "workspace" | readonly string[];

export const LEVEL_LABEL: Record<AccessLevel, string> = { 0: "None", 1: "Read", 2: "Write", 3: "Admin" };
export const LEVEL_TEXT: Record<AccessLevel, string> = {
	0: "text-fg3",
	1: "text-fg2",
	2: "text-info-fg",
	3: "text-warn-fg",
};

export const NEEDS_WRITE = "Needs write access to this resource";
export const NEEDS_ADMIN = "Needs admin access to this resource";

export function scopeLevel(scope: Scope): AccessLevel {
	switch (scope) {
		case Scope.READ:
			return 1;
		case Scope.WRITE:
			return 2;
		case Scope.ADMIN:
			return 3;
		case Scope.MEMBER:
			return 0;
		case Scope.UNSPECIFIED:
			return 0;
	}
}

export function levelOn(scopes: readonly EntityScope[], type: EntityType, id: string): AccessLevel {
	let best: AccessLevel = 0;
	for (const s of scopes) {
		const matches = s.entityType === EntityType.SYSTEM || (s.entityType === type && s.entityId === id);
		if (!matches) continue;
		const lvl = scopeLevel(s.scope);
		if (lvl > best) best = lvl;
	}
	return best;
}

export function hasOrganizationMembership(scopes: readonly EntityScope[], orgId: string): boolean {
	return scopes.some(
		(scope) =>
			scope.scope !== Scope.UNSPECIFIED &&
			(scope.entityType === EntityType.SYSTEM ||
				(scope.entityType === EntityType.ORGANIZATION && scope.entityId === orgId)),
	);
}

/** True when a grant on the organization or workspace, or a system grant (admin holds every scope), gives scope. */
export function holdsScope(scopes: readonly EntityScope[], orgId: string, workspaceId: string, scope: Scope): boolean {
	return scopes.some((s) => {
		const isSystem = s.entityType === EntityType.SYSTEM;
		const coversScope = s.scope === scope || (isSystem && s.scope === Scope.ADMIN);
		const coversOrg = s.entityType === EntityType.ORGANIZATION && s.entityId === orgId;
		const coversWorkspace = s.entityType === EntityType.WORKSPACE && s.entityId === workspaceId;
		return coversScope && (isSystem || coversOrg || coversWorkspace);
	});
}

export function useMyScopes({ enabled = true }: { enabled?: boolean } = {}) {
	const { data, isLoading } = useQuery(getScopes, {}, { enabled });
	const scopes = data?.scopes ?? [];

	const orgLevel = (orgId: string): AccessLevel => levelOn(scopes, EntityType.ORGANIZATION, orgId);
	const workspaceLevel = (orgId: string, workspaceId: string): AccessLevel => {
		const ws = levelOn(scopes, EntityType.WORKSPACE, workspaceId);
		const org = orgLevel(orgId);
		return ws > org ? ws : org;
	};

	const resourceLevel = (orgId: string, workspaceId: string, resourceId: string): AccessLevel => {
		const res = levelOn(scopes, EntityType.RESOURCE, resourceId);
		const ws = workspaceLevel(orgId, workspaceId);
		return res > ws ? res : ws;
	};

	const holds = (orgId: string, workspaceId: string, scope: Scope): boolean =>
		holdsScope(scopes, orgId, workspaceId, scope);

	const resourceGrants = (orgId: string, workspaceId: string): ResourceGrants | undefined => {
		if (isLoading) return undefined;
		if (workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.READ)) return "workspace";
		const ids = scopes.filter((s) => s.entityType === EntityType.RESOURCE && scopeLevel(s.scope) > 0);
		return [...new Set(ids.map((s) => s.entityId))];
	};

	return {
		isLoading,
		scopes,
		orgLevel,
		workspaceLevel,
		resourceLevel,
		holds,
		resourceGrants,
		orgMember: (orgId: string) => hasOrganizationMembership(scopes, orgId),
	};
}
