import { useQuery } from "@connectrpc/connect-query";
import { listOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { keepPreviousData } from "@tanstack/react-query";
import { useState } from "react";

// An organization filter lists this many of the newest organizations matching the typed text.
const FILTER_ORGS = 20;

/** The choices of an organization filter for an operator: the newest organizations matching the typed text. */
export function useOrgFilterChoices(enabled: boolean) {
	const [search, setSearch] = useState("");
	const listed = useQuery(
		listOrgs,
		{ pageSize: FILTER_ORGS, query: search.trim() },
		{ enabled, placeholderData: keepPreviousData },
	);
	const orgs = (listed.data?.orgs ?? []).flatMap((o) => (o.organization === undefined ? [] : [o.organization]));
	return { orgs, onSearch: setSearch };
}
