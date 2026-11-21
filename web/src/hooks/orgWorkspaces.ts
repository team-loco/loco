import { keepPreviousData } from "@tanstack/react-query";
import { useQuery } from "@connectrpc/connect-query";
import { getWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { useState } from "react";

// A workspace filter lists this many of the newest workspaces matching the typed text.
const FILTER_WORKSPACES = 20;

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
