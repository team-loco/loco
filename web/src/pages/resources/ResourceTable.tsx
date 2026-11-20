import { functionalUpdate } from "@tanstack/react-table";
import { GlobeIcon, SearchXIcon, XIcon } from "lucide-react";
import { useMemo } from "react";
import { useQueryStates } from "nuqs";
import { useLocation, useNavigate } from "react-router";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { Button } from "@/components/design/Button";
import { DataTable, DataTablePager, pageWithin, useDataTable } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { ResourceCellContext, resourceColumns, rowPath } from "./columns";
import {
	ALL,
	FILTER_URL_KEYS,
	filterRows,
	isDraftRow,
	nameOrder,
	PAGE_SIZES,
	regionCounts,
	regionTotal,
	visibleStatusFilters,
	statusCounts,
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

	const counts = statusCounts(rows, filter);
	const regionOptions = [
		{ value: ALL, label: "All regions", count: regionTotal(rows, filter) },
		...regionCounts(rows, filter).map((r) => ({
			value: r.region,
			label: r.region,
			count: r.count,
		})),
	];
	const hasFilters = filter.query.trim() !== "" || filter.status !== ALL || filter.region !== ALL;

	const search = (
		<SearchInput
			value={filter.query}
			placeholder="Search name, image, version"
			aria-label="Search resources"
			onChange={(e) => {
				setFilter({ query: e.target.value });
			}}
			onClear={() => {
				setFilter({ query: "" });
			}}
		/>
	);
	const filters = (
		<>
			<ToggleGroup
				variant="segmented"
				aria-label="Filter by status"
				className="max-w-full overflow-x-auto"
				value={[filter.status]}
				onValueChange={(v: string[]) => {
					if (v[0] !== undefined) setFilter({ status: v[0] });
				}}
			>
				<ToggleGroupItem value={ALL} count={counts.get(ALL)}>
					All
				</ToggleGroupItem>
				{visibleStatusFilters(counts, filter.status).map((f) => (
					<ToggleGroupItem key={f.value} value={f.value} count={counts.get(f.value)}>
						{f.label}
					</ToggleGroupItem>
				))}
			</ToggleGroup>
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
		<Section filters={filters} search={search}>
			<ResourceCellContext value={{ orgId, workspaceId, environments, active, nowMs }}>
				<DataTable
					table={table}
					label="Resources"
					onRowClick={(row) => void navigate(rowPath(row.original, orgId, workspaceId, locationSearch))}
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
