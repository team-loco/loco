import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQueries } from "@tanstack/react-query";
import { listOrgResources, listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { EntityType } from "@gen/loco/token/v1/token_pb";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";

import { useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";

import { entityNode } from "./model";
import type { EntityNode } from "./model";

// Each list the picker sends asks for one page of this many.
export const CHOICE_PAGE = 40;
// A workspace/resource search lists the resources of at most this many matching workspaces.
const SEARCHED_WORKSPACES = 3;

export interface AccessChoices {
	workspaces: EntityNode[];
	resources: EntityNode[];
	/** How many more match than the lists hold. */
	more: number;
	workspacesLoading: boolean;
	resourcesLoading: boolean;
	error: unknown;
	/** The message for an error that carries none. */
	errorFallback: string;
	retry: () => void;
}

/**
 * The workspaces and resources the access picker offers for its typed text. Workspaces come from one page of
 * the organization's, searched by the text before any slash. Resources come from one page of the
 * organization's searched by the text, or, for a workspace owner or after a slash, from one page of each target
 * workspace's searched by the text after the slash: the owner's workspace, or the first few workspaces whose
 * names match, one named exactly first.
 */
export function useAccessChoices({
	org,
	scope,
	text,
}: {
	org: EntityNode | null;
	scope: EntityNode | null;
	text: string;
}): AccessChoices {
	const transport = useTransport();
	const scopes = useMyScopes();
	const slash = text.indexOf("/");
	const wsText = (slash === -1 ? text : text.slice(0, slash)).trim();
	const resText = (slash === -1 ? text : text.slice(slash + 1)).trim();
	const owned = scope?.kind === "ws" ? scope : null;

	const orgId = org?.id ?? "";
	const listsWorkspaces = owned === null && org !== null && !scopes.isLoading && scopes.orgMember(orgId);
	const wsQuery = useQuery(
		listOrgWorkspaces,
		{ orgId, pageSize: CHOICE_PAGE, query: wsText, includeTotalCount: true },
		{ enabled: listsWorkspaces, placeholderData: keepPreviousData },
	);
	const searchesOrg = listsWorkspaces && slash === -1;
	const orgResQuery = useQuery(
		listOrgResources,
		{ orgId, pageSize: CHOICE_PAGE, query: resText, includeTotalCount: true },
		{ enabled: searchesOrg, placeholderData: keepPreviousData },
	);
	const orgResources = searchesOrg
		? (orgResQuery.data?.resources ?? []).map((r) =>
				entityNode(
					EntityType.RESOURCE,
					r.id,
					r.name,
					entityNode(EntityType.WORKSPACE, r.workspaceId, r.workspaceName, org),
				),
			)
		: [];
	const listed =
		owned !== null
			? [owned]
			: (wsQuery.data?.workspaces ?? []).map((w) =>
					entityNode(EntityType.WORKSPACE, w.id, w.name, org, w.resourceCount),
				);
	const named = (n: EntityNode) => n.label.toLowerCase().includes(wsText.toLowerCase());
	const targets = ((): EntityNode[] => {
		if (owned !== null) return slash === -1 || named(owned) ? [owned] : [];
		if (slash === -1) return [];
		const exact = (w: EntityNode) => (w.label.toLowerCase() === wsText.toLowerCase() ? 0 : 1);
		return listed
			.filter(named)
			.toSorted((a, b) => exact(a) - exact(b))
			.slice(0, SEARCHED_WORKSPACES);
	})();
	const grants = targets.map((w) => scopes.resourceGrants(w.parent?.id ?? orgId, w.id));

	const resourceQueries = useQueries({
		queries: targets.map((w, i) => {
			const granted = grants[i];
			if (granted === undefined || granted === "workspace") {
				return {
					...createQueryOptions(
						listWorkspaceResources,
						{ workspaceId: w.id, pageSize: CHOICE_PAGE, query: resText, includeTotalCount: true },
						{ transport },
					),
					enabled: granted !== undefined,
				};
			}
			return workspaceResourcesOptions(transport, w.id, granted);
		}),
	});
	const resources = targets.flatMap((w, i) => {
		const listedResources = resourceQueries[i]?.data?.resources ?? [];
		// A caller who cannot read the workspace gets just its granted resources, searched here.
		const matching = Array.isArray(grants[i])
			? listedResources.filter((r) => r.name.toLowerCase().includes(resText.toLowerCase()))
			: listedResources;
		return matching.map((r) => entityNode(EntityType.RESOURCE, r.id, r.name, w));
	});
	const resourcesMore = [searchesOrg ? orgResQuery.data : undefined, ...resourceQueries.map((q) => q.data)].reduce(
		(sum, page) => sum + (page === undefined ? 0 : Math.max(0, page.totalCount - page.resources.length)),
		0,
	);
	const wsPage = wsQuery.data;
	const workspacesMore =
		slash !== -1 || owned !== null || wsPage === undefined
			? 0
			: Math.max(0, wsPage.totalCount - wsPage.workspaces.length);

	const resourceLists = searchesOrg ? [orgResQuery, ...resourceQueries] : resourceQueries;
	const resourcesError = resourceLists.find((q) => q.error !== null)?.error ?? null;
	return {
		workspaces: slash !== -1 ? [] : owned !== null ? listed.filter(named) : listed,
		resources: [...orgResources, ...resources],
		more: workspacesMore + resourcesMore,
		workspacesLoading: listsWorkspaces && wsQuery.isPending,
		resourcesLoading: resourceLists.some((q) => q.isPending && q.fetchStatus !== "idle"),
		error: wsQuery.error ?? resourcesError,
		errorFallback: wsQuery.error === null ? "Failed to load resources" : "Failed to load workspaces",
		retry: () => {
			for (const q of [wsQuery, ...resourceLists]) if (q.error !== null) void q.refetch();
		},
	};
}
