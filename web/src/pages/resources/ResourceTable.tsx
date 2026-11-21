import { useQuery as useConnectQuery, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { functionalUpdate } from "@tanstack/react-table";
import { GlobeIcon, SearchXIcon, XIcon } from "lucide-react";
import { useMemo } from "react";
import { useQueryStates } from "nuqs";
import { useLocation, useNavigate } from "react-router";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { Button } from "@/components/design/Button";
import { DataTable, useDataTable } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { useTokenPage } from "@/hooks/useTokenPage";
import { resourcesPollMs, useCurrentDeployments } from "@/hooks/useWorkspaceDeployments";
import { tokenPageLabel, tokenPager } from "@/lib/paging";
import { userLabel } from "@/lib/user";

import { ResourceCellContext, resourceColumns, rowPath } from "./columns";
import { resourcesOrder, shownDrafts, statusCounts, statusInput } from "./query";
import { ResourceSearch, StatusFilter } from "./ResourceFilters";
import {
	ALL,
	buildRows,
	DRAFT,
	FILTER_URL_KEYS,
	isDraftRow,
	PAGE_SIZES,
	tableParsers,
	type DraftRow,
	type ListRow,
} from "./table";

function rowId(row: ListRow): string {
	return isDraftRow(row) ? row.draft.id : row.resource.id;
}

export function ResourceTable({
	drafts,
	environments,
	active,
	orgId,
	workspaceId,
	nowMs,
}: {
	drafts: DraftRow[];
	environments: Environment[];
	active: Environment | undefined;
	orgId: string;
	workspaceId: string;
	nowMs: number;
}) {
	const navigate = useNavigate();
	const { search: locationSearch } = useLocation();
	const transport = useTransport();
	const { workspaceLevel } = useMyScopes();
	const members = useQuery({
		...workspaceMembersOptions(transport, workspaceId),
		enabled: workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.READ),
	});
	const creators = new Map(
		(members.data?.members ?? []).map((m) => [m.userId, userLabel({ name: m.userName, email: m.userEmail })]),
	);
	const columns = useMemo(() => resourceColumns(environments), [environments]);
	const parsers = useMemo(() => tableParsers(columns), [columns]);
	const [state, setState] = useQueryStates(parsers, {
		history: "replace",
		urlKeys: FILTER_URL_KEYS,
	});
	const filter = {
		query: state.query,
		status: state.status,
		region: state.region,
	};

	const setFilter = (patch: Partial<typeof filter>) => {
		void setState({ ...patch, page: null });
	};
	const clearFilters = () => {
		void setState({ query: null, status: null, region: null, page: null });
	};

	// The counts ignore the status filter, as the region counts do.
	const { environmentId, ...order } = resourcesOrder(state.sort, environments);
	const counted = {
		workspaceId,
		query: filter.query.trim(),
		region: filter.region === ALL ? "" : filter.region,
		environmentId,
	};
	const listed = useTokenPage(
		listWorkspaceResources,
		{ ...counted, ...order, ...statusInput(filter.status), pageSize: state.size },
		state.page,
		{ placeholderData: keepPreviousData, refetchInterval: (q) => resourcesPollMs(q.state.data?.resources) },
		{ ...counted, includeFacets: true },
		() => void setState({ page: null }),
	);
	const regionsTotal = useConnectQuery(
		listWorkspaceResources,
		{ ...counted, region: "", pageSize: 1, includeTotalCount: true },
		{ enabled: filter.region !== ALL },
	).data?.totalCount;
	const resources = listed.query.data?.resources ?? [];
	const deployments = useCurrentDeployments(resources);
	const draftsOnly = filter.status === DRAFT;
	const serverRows = draftsOnly ? [] : buildRows(resources, deployments, environments, active?.id);
	const draftCount = shownDrafts(drafts, { ...filter, status: DRAFT }).length;
	const data: ListRow[] = [...(state.page.length === 0 ? shownDrafts(drafts, filter) : []), ...serverRows];
	const facets = listed.counts?.facets;
	const counts = statusCounts(listed.total, facets, draftCount);
	const matched = facets === undefined && !draftsOnly ? undefined : (counts.get(filter.status) ?? 0);

	const sorting = [state.sort];
	const table = useDataTable("resources", {
		columns,
		data,
		getRowId: rowId,
		state: { sorting, columnVisibility: state.cols },
		manualSorting: true,
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) void setState({ sort: next, page: null });
		},
		onColumnVisibilityChange: (updater) => {
			void setState({ cols: functionalUpdate(updater, state.cols) });
		},
	});
	const pager = tokenPager(state.page, draftsOnly ? "" : listed.nextPageToken, (page) => {
		if (!listed.query.isPlaceholderData) void setState({ page });
	});

	const allRegions = filter.region === ALL ? listed.total : regionsTotal;
	const regionOptions = [
		{ value: ALL, label: "All regions", ...(allRegions === undefined ? {} : { count: allRegions }) },
		...(facets?.regions ?? []).map((r) => ({
			value: r.region,
			label: r.region,
			count: r.count,
		})),
	];
	if (!regionOptions.some((o) => o.value === filter.region))
		regionOptions.push({ value: filter.region, label: filter.region, count: 0 });
	const hasFilters = filter.query.trim() !== "" || filter.status !== ALL || filter.region !== ALL;

	const filters = (
		<>
			<StatusFilter
				counts={counts}
				status={filter.status}
				onStatus={(status) => {
					setFilter({ status });
				}}
			/>
			<FilterMenu
				label="Region"
				icon={<GlobeIcon />}
				mono
				value={filter.region}
				options={regionOptions}
				onChange={(region) => {
					setFilter({ region });
				}}
			/>
		</>
	);

	return (
		<Section
			filters={filters}
			search={
				<ResourceSearch
					query={filter.query}
					onQuery={(query) => {
						setFilter({ query });
					}}
				/>
			}
		>
			{listed.error === null ? (
				<>
					<ResourceCellContext value={{ orgId, workspaceId, environments, active, nowMs, creators }}>
						<DataTable
							table={table}
							label="Resources"
							loading={listed.query.isPending || listed.resetting}
							onRowClick={(row) => void navigate(rowPath(row.original, orgId, workspaceId, locationSearch))}
							rowAction="navigate"
							className="[--row-h:62px]"
							empty={
								<EmptyState
									icon={<SearchXIcon />}
									title="No resources match"
									query={filter.query.trim()}
									action={
										hasFilters && (
											<Button variant="outline" onClick={clearFilters}>
												<XIcon />
												Clear filters
											</Button>
										)
									}
								/>
							}
						/>
					</ResourceCellContext>
					<Pager
						{...pager}
						label={tokenPageLabel(pager.page, state.size, draftsOnly ? data.length : serverRows.length, matched)}
						total={data.length}
						pageSizes={PAGE_SIZES}
						pageSize={state.size}
						onPageSize={(size) => {
							void setState({ size: PAGE_SIZES.find((n) => n === size) ?? null, page: null });
						}}
					/>
				</>
			) : (
				<ErrorState error={listed.error} what="resources" rpc={listWorkspaceResources} onRetry={listed.query.refetch} />
			)}
		</Section>
	);
}
