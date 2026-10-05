import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { getScopes, listTokens } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType } from "@gen/loco/token/v1/token_pb";
import type { Token } from "@gen/loco/token/v1/token_pb";

import { useAuth } from "@/auth/AuthProvider";
import { useOrgWorkspace } from "@/context/ContextProvider";

import { buildHeld, holds, nodeKey, withScopeEntities } from "./model";
import type { EntityNode, EntityTree, HeldScopes, OwnerKey } from "./model";

export interface Owner {
	key: OwnerKey;
	entityType: EntityType;
	id: string | null;
	label: string;
	kind: string;
	noun: string;
	canList: boolean;
	canCreate: boolean;
}

export interface OwnerTokens {
	owner: Owner;
	tokens: Token[];
	isLoading: boolean;
	error: unknown;
}

export interface TokenData {
	owners: Record<OwnerKey, OwnerTokens>;
	tree: EntityTree;
	held: HeldScopes;
	userId: string | null;
	scopesLoading: boolean;
	scopesError: unknown;
	resourcesLoading: boolean;
}

function buildTree(
	orgs: { id: string; name: string }[],
	activeOrgId: string | null,
	workspaces: { id: string; name: string; orgId: string }[],
	resources: { id: string; name: string; workspaceId: string }[],
	userId: string | null,
): EntityTree {
	const order: EntityNode[] = [];
	const childCount = new Map<string, number>();
	const push = (node: EntityNode) => {
		order.push(node);
		if (node.parent !== null) childCount.set(node.parent, (childCount.get(node.parent) ?? 0) + 1);
	};
	if (userId !== null) {
		push({ key: nodeKey(EntityType.USER, userId), kind: "user", entityType: EntityType.USER, id: userId, label: "Your account", parent: null });
	}
	const sortedOrgs = orgs.toSorted((a, b) => Number(b.id === activeOrgId) - Number(a.id === activeOrgId));
	for (const o of sortedOrgs) {
		push({ key: nodeKey(EntityType.ORGANIZATION, o.id), kind: "org", entityType: EntityType.ORGANIZATION, id: o.id, label: o.name, parent: null });
	}
	for (const w of workspaces) {
		push({
			key: nodeKey(EntityType.WORKSPACE, w.id),
			kind: "ws",
			entityType: EntityType.WORKSPACE,
			id: w.id,
			label: w.name,
			parent: nodeKey(EntityType.ORGANIZATION, w.orgId),
		});
	}
	for (const r of resources) {
		push({
			key: nodeKey(EntityType.RESOURCE, r.id),
			kind: "res",
			entityType: EntityType.RESOURCE,
			id: r.id,
			label: r.name,
			parent: nodeKey(EntityType.WORKSPACE, r.workspaceId),
		});
	}
	const nodes = new Map(order.map((n) => [n.key, n]));
	return { nodes, order, childCount };
}

export function useTokenData(): TokenData {
	const transport = useTransport();
	const { user } = useAuth();
	const { activeOrgId, activeWorkspaceId, orgs, workspaces } = useOrgWorkspace();
	const userId = user?.id ?? null;

	const scopesQuery = useQuery(getScopes, {});
	const orgWorkspaces = workspaces.filter((w) => w.orgId === activeOrgId);

	const resourceQueries = useQueries({
		queries: orgWorkspaces.map((w) =>
			createQueryOptions(listWorkspaceResources, { workspaceId: w.id, pageSize: 200 }, { transport }),
		),
	});
	const resources = resourceQueries.flatMap((q) => q.data?.resources ?? []);
	const resourcesLoading = resourceQueries.some((q) => q.isLoading);

	const tree = buildTree(orgs, activeOrgId, orgWorkspaces, resources, userId);
	const scopeList = scopesQuery.data?.scopes ?? [];
	const held = buildHeld(scopeList);

	const activeOrg = orgs.find((o) => o.id === activeOrgId);
	const activeWs = workspaces.find((w) => w.id === activeWorkspaceId);

	const can = (entityType: EntityType, id: string | null, level: 1 | 2): boolean => {
		if (id === null || scopesQuery.data === undefined) return false;
		const key = nodeKey(entityType, id);
		return holds(held, tree, key, level);
	};

	const mkOwner = (key: OwnerKey, entityType: EntityType, id: string | null, label: string, kind: string, noun: string): Owner => ({
		key,
		entityType,
		id,
		label,
		kind,
		noun,
		canList: can(entityType, id, 1),
		canCreate: can(entityType, id, 2),
	});

	const orgOwner = mkOwner("org", EntityType.ORGANIZATION, activeOrgId, activeOrg?.name ?? "Organization", "Organization", "organization");
	const wsOwner = mkOwner("workspace", EntityType.WORKSPACE, activeWorkspaceId, activeWs?.name ?? "Workspace", "Workspace", "workspace");
	const personalOwner = mkOwner("personal", EntityType.USER, userId, "Personal", "You", "personal");

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
		isLoading: scopesQuery.isLoading || (owner.canList && q.isLoading),
		error: q.error,
	});

	const owners: Record<OwnerKey, OwnerTokens> = {
		org: wrap(orgOwner, orgTokens),
		workspace: wrap(wsOwner, wsTokens),
		personal: wrap(personalOwner, personalTokens),
	};

	const tokenScopes = Object.values(owners).flatMap((o) => o.tokens.flatMap((t) => t.scopes));
	const fullTree = withScopeEntities(tree, tokenScopes);

	return {
		owners,
		tree: fullTree,
		held,
		userId,
		scopesLoading: scopesQuery.isLoading,
		scopesError: scopesQuery.error,
		resourcesLoading,
	};
}
