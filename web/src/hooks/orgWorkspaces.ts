import { queryOptions, keepPreviousData } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import { getWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { ListOrgWorkspacesResponseSchema, WorkspaceService } from "@gen/loco/workspace/v1/workspace_pb";
import { useState } from "react";
import { collectPages } from "@/lib/collectPages";

const WORKSPACE_PAGE_SIZE = 200;
// A workspace filter lists this many of the newest workspaces matching the typed text.
const FILTER_WORKSPACES = 20;

export function orgWorkspacesOptions(transport: Transport, orgId: string) {
	const input = { orgId, pageSize: WORKSPACE_PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listOrgWorkspaces, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(WorkspaceService, transport);
			const workspaces = await collectPages(async (pageToken) => {
				const page = await client.listOrgWorkspaces({ ...input, pageToken }, { signal });
				return { items: page.workspaces, nextPageToken: page.nextPageToken };
			});
			return create(ListOrgWorkspacesResponseSchema, { workspaces });
		},
		enabled: orgId !== "",
	});
}

/** The choices of a workspace filter: the newest workspaces matching the typed text, with the selected one first when it is not among them. */
export function useWorkspaceFilterChoices(orgId: string, selectedId: string, enabled = true) {
	const [search, setSearch] = useState("");
	const on = enabled && orgId !== "";
	const newest = useQuery(
		listOrgWorkspaces,
		{ orgId, pageSize: FILTER_WORKSPACES, query: search.trim() },
		{ enabled: on, placeholderData: keepPreviousData },
	);
	const picked = useQuery(getWorkspace, { workspaceId: selectedId }, { enabled: on && selectedId !== "" });
	const selected = picked.data?.workspace?.orgId === orgId ? picked.data.workspace : undefined;
	const listed = newest.data?.workspaces ?? [];
	const workspaces =
		selected === undefined || listed.some((w) => w.id === selected.id) ? listed : [selected, ...listed];
	return { workspaces, onSearch: setSearch, selectedLoading: on && selectedId !== "" && picked.isLoading };
}
