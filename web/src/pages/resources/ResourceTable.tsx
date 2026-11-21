import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { functionalUpdate } from "@tanstack/react-table";
import { GlobeIcon, SearchXIcon, XIcon } from "lucide-react";
import { useMemo } from "react";
import { useQueryStates } from "nuqs";
import { useLocation, useNavigate } from "react-router";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { Button } from "@/components/design/Button";
import { DataTable, DataTablePager, pageWithin, useDataTable } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { userLabel } from "@/lib/user";

import { ResourceCellContext, resourceColumns, rowPath } from "./columns";
import { ResourceSearch, StatusFilter } from "./ResourceFilters";
import {
	ALL,
	FILTER_URL_KEYS,
	filterRows,
	isDraftRow,
	nameOrder,
	PAGE_SIZES,
	regionCounts,
	regionTotal,
	tableParsers,
	type ListRow,
} from "./table";

function rowId(row: ListRow): string {
	return isDraftRow(row) ? row.draft.id : row.resource.id;
}

export function ResourceTable({
	rows,
	environments,
	active,
	orgId,
	workspaceId,
	nowMs,
}: {
	rows: ListRow[];
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

	const data = nameOrder(filterRows(rows, filter));
	const sorting = [state.sort];
	const pagination = pageWithin({ pageIndex: state.page, pageSize: state.size }, data.length);
	const table = useDataTable("resources", {
		columns,
		data,
		getRowId: rowId,
		state: { sorting, columnVisibility: state.cols, pagination },
		manualPagination: false,
		autoResetPageIndex: false,
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) void setState({ sort: next, page: null });
		},
		onColumnVisibilityChange: (updater) => {
			void setState({ cols: functionalUpdate(updater, state.cols) });
		},
		onPaginationChange: (updater) => {
			const next = functionalUpdate(updater, pagination);
			void setState({
				page: next.pageIndex,
				size: PAGE_SIZES.find((n) => n === next.pageSize) ?? null,
			});
		},
	});

	const regionOptions = [
		{ value: ALL, label: "All regions", count: regionTotal(rows, filter) },
		...regionCounts(rows, filter).map((r) => ({
			value: r.region,
			label: r.region,
			count: r.count,
		})),
	];
	const hasFilters = filter.query.trim() !== "" || filter.status !== ALL || filter.region !== ALL;

	const filters = (
		<>
			<StatusFilter
				rows={rows}
				filter={filter}
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
			<ResourceCellContext value={{ orgId, workspaceId, environments, active, nowMs, creators }}>
				<DataTable
					table={table}
					label="Resources"
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
			<DataTablePager table={table} pageSizes={PAGE_SIZES} />
		</Section>
	);
}
