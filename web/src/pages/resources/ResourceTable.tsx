import { functionalUpdate, useTable, type RowSelectionState } from "@tanstack/react-table";
import { SearchXIcon, XIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { Button } from "@/components/design/Button";
import { DataTable, dataTableFeatures, DataTablePager, pageWithin, sortOptions } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { BulkActions } from "./BulkActions";
import { ResourceCellContext, resourceColumns } from "./columns";
import {
	ALL,
	filterRows,
	isDraftRow,
	nameOrder,
	PAGE_SIZES,
	readTableState,
	regionCounts,
	STATUS_FILTERS,
	statusCounts,
	writeTableState,
	type ListRow,
	type ResourceRow,
	type TableState,
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
	const [params, setParams] = useSearchParams();
	const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
	const columns = useMemo(() => resourceColumns(environments), [environments]);
	const state = readTableState(params, columns);
	const { filter } = state;

	const update = (patch: Partial<TableState>) => {
		setParams((prev) => writeTableState(prev, { ...readTableState(prev, columns), ...patch }, columns), {
			replace: true,
		});
	};
	const setFilter = (patch: Partial<TableState["filter"]>) => {
		update({ filter: { ...filter, ...patch }, page: 0 });
	};
	const clearFilters = () => {
		update({ filter: { query: "", status: ALL, region: ALL }, page: 0 });
	};

	const data = nameOrder(filterRows(rows, filter));
	const sorting = [state.sorting];
	const pagination = pageWithin({ pageIndex: state.page, pageSize: state.size }, data.length);
	const table = useTable({
		...sortOptions,
		features: dataTableFeatures,
		columns,
		data,
		getRowId: rowId,
		enableRowSelection: (row) => !isDraftRow(row.original),
		state: { sorting, columnVisibility: state.visibility, pagination, rowSelection },
		autoResetPageIndex: false,
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) update({ sorting: next, page: 0 });
		},
		onColumnVisibilityChange: (updater) => {
			update({ visibility: functionalUpdate(updater, state.visibility) });
		},
		onPaginationChange: (updater) => {
			const next = functionalUpdate(updater, pagination);
			update({ page: next.pageIndex, size: next.pageSize });
		},
		onRowSelectionChange: setRowSelection,
	});

	const counts = statusCounts(rows, filter);
	const regionOptions = [
		{ value: ALL, label: "All regions", count: filterRows(rows, { ...filter, status: ALL, region: ALL }).length },
		...regionCounts(rows, filter).map((r) => ({ value: r.region, label: r.region, count: r.count })),
	];
	const selectedRows = table
		.getRowModel()
		.rows.map((row) => row.original)
		.filter((row): row is ResourceRow => !isDraftRow(row) && row.resource.id in rowSelection);
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
				{STATUS_FILTERS.map((f) => (
					<ToggleGroupItem key={f.value} value={f.value} count={counts.get(f.value)}>
						{f.label}
					</ToggleGroupItem>
				))}
			</ToggleGroup>
			<FilterMenu
				label="Region"
				value={filter.region}
				options={regionOptions}
				onChange={(region) => {
					setFilter({ region });
				}}
			/>
		</>
	);

	const bulk = (
		<BulkActions
			rows={selectedRows}
			environment={active}
			onClear={() => {
				setRowSelection({});
			}}
			onDone={(failed) => {
				setRowSelection(Object.fromEntries([...failed].map((id) => [id, true as const])));
			}}
		/>
	);
	const some = selectedRows.length > 0;

	return (
		<Section filters={some ? bulk : filters} search={some ? undefined : search}>
			<ResourceCellContext value={{ orgId, workspaceId, environments, active, nowMs, search: params }}>
				<DataTable
					table={table}
					className="table-fixed"
					style={{ minWidth: table.getTotalSize() }}
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
