import { useQuery } from "@connectrpc/connect-query";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType, Scope, type EntityScope } from "@gen/loco/token/v1/token_pb";

export type AccessLevel = 0 | 1 | 2 | 3;

export function scopeLevel(scope: Scope): AccessLevel {
	switch (scope) {
		case Scope.READ:
			return 1;
		case Scope.WRITE:
			return 2;
		case Scope.ADMIN:
			return 3;
		case Scope.UNSPECIFIED:
			return 0;
	}
}

function levelOn(scopes: EntityScope[], type: EntityType, id: string): AccessLevel {
	let best: AccessLevel = 0;
	for (const s of scopes) {
		const matches = s.entityType === EntityType.SYSTEM || (s.entityType === type && s.entityId === id);
		if (!matches) continue;
		const lvl = scopeLevel(s.scope);
		if (lvl > best) best = lvl;
	}
	return best;
}

export function useMyScopes() {
	const { data, isLoading } = useQuery(getScopes, {});
	const scopes = data?.scopes ?? [];

	const orgLevel = (orgId: string): AccessLevel => levelOn(scopes, EntityType.ORGANIZATION, orgId);
	const workspaceLevel = (orgId: string, workspaceId: string): AccessLevel => {
		const ws = levelOn(scopes, EntityType.WORKSPACE, workspaceId);
		const org = orgLevel(orgId);
		return ws > org ? ws : org;
	};

	const holds = (orgId: string, workspaceId: string, scope: Scope): boolean =>
		scopes.some(
			(s) =>
				s.scope === scope &&
				(s.entityType === EntityType.SYSTEM ||
					(s.entityType === EntityType.ORGANIZATION && s.entityId === orgId) ||
					(s.entityType === EntityType.WORKSPACE && s.entityId === workspaceId)),
		);

	return { isLoading, scopes, orgLevel, workspaceLevel, holds };
}
