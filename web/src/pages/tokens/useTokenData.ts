import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { listOrgResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { getScopes, listTokens } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType } from "@gen/loco/token/v1/token_pb";
import type { EntityScope, Token } from "@gen/loco/token/v1/token_pb";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { useAuth } from "@/auth/AuthProvider";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { MAX_PAGE, useWorkspace } from "@/hooks/useWorkspace";

import { buildHeld, entityNode, holds, withScopeEntities } from "./model";
import type { Entities, EntityNode, HeldScopes, OwnerKey } from "./model";

export interface Owner {
	key: OwnerKey;
	entityType: EntityType;
	id: string | null;
	node: EntityNode | null;
	label: string;
	canList: boolean;
	canCreate: boolean;
	createHint: string | null;
}

interface OwnerTokens {
	owner: Owner;
	tokens: Token[];
	isLoading: boolean;
	error: unknown;
	refetch: () => Promise<unknown>;
}

export interface TokenData {
	owners: Record<OwnerKey, OwnerTokens>;
	/** The signed-in user's account and organizations, which the access picker always offers. */
	roots: EntityNode[];
	activeOrg: EntityNode | null;
	/** The entities the page looked up or knows, with their parents where the caller can read them. */
	known: Entities;
	/** known, plus a stand-in labelled by a short ID for each scope entity the page could not look up. */
	entities: Entities;
	/** Whether a scope's entity or its parent is still being looked up. */
	entitiesLoading: boolean;
	held: HeldScopes;
	scopesLoading: boolean;
	scopesError: unknown;
}

export function useTokenData(): TokenData {
	const transport = useTransport();
	const { user } = useAuth();
	const { activeOrgId, activeWorkspaceId, orgs } = useOrgWorkspace();
	const userId = user?.id ?? null;

	const scopesQuery = useQuery(getScopes, {});
	const activeWsQuery = useWorkspace(activeOrgId, activeWorkspaceId);
	// Permissions wait for the active workspace as well as the scopes: it inherits its organization's scopes.
	const treeLoading = scopesQuery.isLoading || activeWsQuery.isLoading;

	const userNode = userId === null ? null : entityNode(EntityType.USER, userId, "Your account", null);
	const orgNodes = new Map(
		orgs
			.toSorted((a, b) => Number(b.id === activeOrgId) - Number(a.id === activeOrgId))
			.map((o) => [o.id, entityNode(EntityType.ORGANIZATION, o.id, o.name, null)]),
	);
	const orgOf = (w: Workspace): EntityNode =>
		orgNodes.get(w.orgId) ?? entityNode(EntityType.ORGANIZATION, w.orgId, w.orgName, null);
	const wsNode = (w: Workspace): EntityNode =>
		entityNode(EntityType.WORKSPACE, w.id, w.name, orgOf(w), w.resourceCount);

	const activeOrg = activeOrgId === null ? null : (orgNodes.get(activeOrgId) ?? null);
	const activeWs = activeWsQuery.workspace;
	const activeWorkspace =
		activeWorkspaceId === null
			? null
			: activeWs?.id === activeWorkspaceId
				? wsNode(activeWs)
				: entityNode(EntityType.WORKSPACE, activeWorkspaceId, "Workspace", null);
	const held = buildHeld(scopesQuery.data?.scopes ?? []);

	const can = (node: EntityNode | null, level: 1 | 2 | 3): boolean => {
		if (node === null || scopesQuery.data === undefined || activeWsQuery.isLoading) return false;
		return holds(held, node, level);
	};

	const mkOwner = (key: OwnerKey, entityType: EntityType, node: EntityNode | null, label: string): Owner => {
		const personal = entityType === EntityType.USER;
		const canCreate = can(node, personal ? 2 : 3);
		return {
			key,
			entityType,
			id: node?.id ?? null,
			node,
			label,
			canList: can(node, 1),
			canCreate,
			createHint: canCreate ? null : `You need ${personal ? "write" : "admin"} access to ${label} to create tokens`,
		};
	};

	const orgOwner = mkOwner("org", EntityType.ORGANIZATION, activeOrg, activeOrg?.label ?? "Organization");
	const wsOwner = mkOwner("ws", EntityType.WORKSPACE, activeWorkspace, activeWs?.name ?? "Workspace");
	const personalOwner = mkOwner("user", EntityType.USER, userNode, "Personal");

	const orgTokens = useQuery(
		listTokens,
		orgOwner.id !== null ? { entityType: orgOwner.entityType, entityId: orgOwner.id } : undefined,
		{ enabled: orgOwner.canList },
	);
	const wsTokens = useQuery(
		listTokens,
		wsOwner.id !== null ? { entityType: wsOwner.entityType, entityId: wsOwner.id } : undefined,
		{ enabled: wsOwner.canList },
	);
	const personalTokens = useQuery(
		listTokens,
		personalOwner.id !== null ? { entityType: personalOwner.entityType, entityId: personalOwner.id } : undefined,
		{ enabled: personalOwner.canList },
	);

	const wrap = (owner: Owner, q: typeof orgTokens): OwnerTokens => ({
		owner,
		tokens: q.data?.tokens ?? [],
		isLoading: treeLoading || (owner.canList && q.isLoading),
		error: q.error,
		refetch: async () => await Promise.all([scopesQuery.refetch(), ...(owner.canList ? [q.refetch()] : [])]),
	});

	const owners: Record<OwnerKey, OwnerTokens> = {
		org: wrap(orgOwner, orgTokens),
		ws: wrap(wsOwner, wsTokens),
		user: wrap(personalOwner, personalTokens),
	};

	// Each organization lists by ID the workspaces and resources tokens name there; only personal tokens reach others.
	const tokenScopes = Object.values(owners).flatMap((o) => o.tokens.flatMap((t) => t.scopes));
	const personalScopes = owners.user.tokens.flatMap((t) => t.scopes);
	const idsOf = (scopes: EntityScope[], type: EntityType) => [
		...new Set(scopes.filter((s) => s.entityType === type).map((s) => s.entityId)),
	];
	const batches = (type: EntityType) =>
		[...orgNodes.values()].flatMap((org) => {
			const ids = idsOf(org.id === activeOrgId ? tokenScopes : personalScopes, type).filter(
				(id) => id !== activeWs?.id,
			);
			return Array.from({ length: Math.ceil(ids.length / MAX_PAGE) }, (_, i) => ({
				org,
				ids: ids.slice(i * MAX_PAGE, (i + 1) * MAX_PAGE),
			}));
		});
	const resourceBatches = batches(EntityType.RESOURCE);
	const resourceQueries = useQueries({
		queries: resourceBatches.map(({ org, ids }) => ({
			...createQueryOptions(listOrgResources, { orgId: org.id, resourceIds: ids, pageSize: MAX_PAGE }, { transport }),
			retry: false,
		})),
	});
	const workspaceQueries = useQueries({
		queries: batches(EntityType.WORKSPACE).map(({ org, ids }) => ({
			...createQueryOptions(listOrgWorkspaces, { orgId: org.id, workspaceIds: ids, pageSize: MAX_PAGE }, { transport }),
			retry: false,
		})),
	});
	const resources = resourceBatches.flatMap(({ org }, i) =>
		(resourceQueries[i]?.data?.resources ?? []).map((r) => ({ ...r, org })),
	);
	const workspaces = new Map(
		resources.map((r) => [r.workspaceId, entityNode(EntityType.WORKSPACE, r.workspaceId, r.workspaceName, r.org)]),
	);
	for (const w of workspaceQueries.flatMap((q) => q.data?.workspaces ?? [])) workspaces.set(w.id, wsNode(w));
	if (activeWs !== undefined && activeWorkspace !== null) workspaces.set(activeWs.id, activeWorkspace);
	const known: EntityNode[] = [
		...(userNode === null ? [] : [userNode]),
		...idsOf(tokenScopes, EntityType.SYSTEM).map((id) => entityNode(EntityType.SYSTEM, id, "Platform", null)),
		...orgNodes.values(),
		...workspaces.values(),
		...resources.map((r) => entityNode(EntityType.RESOURCE, r.id, r.name, workspaces.get(r.workspaceId) ?? null)),
	];
	const found = new Map(known.map((n) => [n.key, n]));

	return {
		owners,
		roots: [...(userNode === null ? [] : [userNode]), ...orgNodes.values()],
		activeOrg,
		known: found,
		entities: withScopeEntities(found, tokenScopes),
		entitiesLoading: [...resourceQueries, ...workspaceQueries].some((q) => q.isPending),
		held,
		scopesLoading: treeLoading,
		scopesError: scopesQuery.error,
	};
}
