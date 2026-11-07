import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { getOrg, listInvitations } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { User } from "@gen/loco/org/v1/org_pb";
import { EntityType, type EntityScope } from "@gen/loco/token/v1/token_pb";
import { useQueries, useQuery as useTanstackQuery, useQueryClient } from "@tanstack/react-query";
import { invitationsOptions, orgMembersOptions, workspaceMembersOptions } from "@/hooks/completeMembers";
import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";

import { parseScopeName, sortScopes, toScopeName, type ScopeName } from "./scopes";

export type EntityKind = "system" | "org" | "workspace" | "resource";

export interface ScopeEntry {
	kind: EntityKind;
	id: string;
	label: string;
	parent: string;
	scopes: ScopeName[];
}

export interface Member {
	user: User;
	entries: ScopeEntry[];
}

const KIND_ORDER: Record<EntityKind, number> = { system: 0, org: 1, workspace: 2, resource: 3 };

function addScope(map: Map<string, ScopeEntry>, base: Omit<ScopeEntry, "scopes">, scope: ScopeName | null) {
	if (scope === null) return;
	const key = `${base.kind}:${base.id}`;
	const entry = map.get(key) ?? { ...base, scopes: [] };
	if (!entry.scopes.includes(scope)) entry.scopes = sortScopes([...entry.scopes, scope]);
	map.set(key, entry);
}

function sortEntries(entries: ScopeEntry[]): ScopeEntry[] {
	return entries.toSorted(
		(a, b) =>
			KIND_ORDER[a.kind] - KIND_ORDER[b.kind] || a.parent.localeCompare(b.parent) || a.label.localeCompare(b.label),
	);
}

export function useTeamData(orgId: string, meId: string, myScopes: EntityScope[]) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const enabled = orgId !== "";
	const orgQuery = useQuery(getOrg, { orgId }, { enabled });
	const membersQuery = useTanstackQuery(orgMembersOptions(transport, orgId));
	const invitationsQuery = useTanstackQuery(invitationsOptions(transport, orgId));
	const wsQuery = useTanstackQuery(orgWorkspacesOptions(transport, orgId));
	const org = orgQuery.data?.organization;
	const workspaces = wsQuery.data?.workspaces ?? [];
	const wsName = new Map(workspaces.map((w) => [w.id, w.name]));

	const memberQueries = useQueries({
		queries: workspaces.map((w) => workspaceMembersOptions(transport, w.id)),
	});

	const needResources = myScopes.some((s) => s.entityType === EntityType.RESOURCE);
	const resourceQueries = useQueries({
		queries: (needResources ? workspaces : []).map((w) => workspaceResourcesOptions(transport, w.id)),
	});
	const resources = new Map<string, { name: string; workspace: string }>();
	for (const q of resourceQueries) {
		for (const r of q.data?.resources ?? []) {
			resources.set(r.id, { name: r.name, workspace: wsName.get(r.workspaceId) ?? "" });
		}
	}

	const byUser = new Map<string, Map<string, ScopeEntry>>();
	workspaces.forEach((w, i) => {
		for (const m of memberQueries[i]?.data?.members ?? []) {
			if (m.userId === meId) continue;
			const map = byUser.get(m.userId) ?? new Map<string, ScopeEntry>();
			for (const raw of m.scopes) {
				addScope(map, { kind: "workspace", id: w.id, label: w.name, parent: "" }, parseScopeName(raw));
			}
			byUser.set(m.userId, map);
		}
	});

	const mine = new Map<string, ScopeEntry>();
	for (const s of myScopes) {
		const name = toScopeName(s.scope);
		switch (s.entityType) {
			case EntityType.SYSTEM:
				addScope(mine, { kind: "system", id: s.entityId, label: "System", parent: "" }, name);
				break;
			case EntityType.ORGANIZATION:
				if (s.entityId === orgId)
					addScope(mine, { kind: "org", id: orgId, label: org?.name ?? "Organization", parent: "" }, name);
				break;
			case EntityType.WORKSPACE: {
				const label = wsName.get(s.entityId);
				if (label !== undefined) addScope(mine, { kind: "workspace", id: s.entityId, label, parent: "" }, name);
				break;
			}
			case EntityType.RESOURCE: {
				const r = resources.get(s.entityId);
				if (r) addScope(mine, { kind: "resource", id: s.entityId, label: r.name, parent: r.workspace }, name);
				break;
			}
			case EntityType.USER:
				break;
			case EntityType.UNSPECIFIED:
				break;
		}
	}

	const users = (membersQuery.data?.members ?? []).flatMap((m) => (m.user ? [m.user] : []));
	const members: Member[] = users.map((user) => {
		const map = user.id === meId ? mine : byUser.get(user.id);
		const entries = map ? sortEntries([...map.values()]) : [];
		return { user, entries };
	});

	const invalidateInvitations = async () => {
		await queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listInvitations, cardinality: "finite" }),
		});
	};

	return {
		org,
		members,
		invitations: invitationsQuery.data?.invitations ?? [],
		invitationsLoading: invitationsQuery.isLoading,
		invalidateInvitations,
		workspaces,
		isLoading: orgQuery.isLoading || membersQuery.isLoading,
		scopesLoading:
			wsQuery.isLoading || memberQueries.some((q) => q.isLoading) || resourceQueries.some((q) => q.isLoading),
		error: orgQuery.error ?? membersQuery.error,
	};
}
