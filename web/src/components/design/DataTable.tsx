import {
	columnResizingFeature,
	columnSizingFeature,
	columnVisibilityFeature,
	createPaginatedRowModel,
	createSortedRowModel,
	flexRender,
	metaHelper,
	rowPaginationFeature,
	rowSelectionFeature,
	rowSortingFeature,
	sortFn_basic,
	Subscribe,
	tableFeatures,
	type Column,
	type ColumnDef,
	type Header,
	type PaginationState,
	type ReactTable,
	type RowData,
	type SortDirection,
	type Table as TanStackTable,
} from "@tanstack/react-table";
import { ArrowDownIcon, ArrowUpIcon, Columns3Icon } from "lucide-react";
import { type CSSProperties, type ReactNode } from "react";

import { ColumnResizeHandle } from "@/components/design/ColumnResizeHandle";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Pager } from "@/components/design/Pager";
import { SoonTag } from "@/components/design/SoonTag";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { pageRangeLabel } from "@/lib/paging";
import { cn } from "@/lib/utils";

export interface DataColumnMeta {
	headClassName?: string;
	cellClassName?: string;
	soon?: boolean;
	defaultHidden?: boolean;
}

export const dataTableFeatures = tableFeatures({
	columnVisibilityFeature,
	columnSizingFeature,
	columnResizingFeature,
	rowSelectionFeature,
	rowSortingFeature,
	sortedRowModel: createSortedRowModel(),
	sortFns: { basic: sortFn_basic },
	rowPaginationFeature,
	paginatedRowModel: createPaginatedRowModel(),
	columnMeta: metaHelper<DataColumnMeta>(),
});
type Features = typeof dataTableFeatures;
export type DataColumn<T extends RowData> = ColumnDef<Features, T> & {
	id: string;
};
export type DataTableInstance<T extends RowData> = TanStackTable<Features, T>;

export const sortOptions = {
	sortDescFirst: false,
	enableSortingRemoval: false,
	enableMultiSort: false,
} as const;

export function columnLabel<T extends RowData>(column: Column<Features, T>): string {
	const header = column.columnDef.header;
	return typeof header === "string" ? header : column.id;
}

export function pageWithin(pagination: PaginationState, rowCount: number): PaginationState {
	const last = Math.max(0, Math.ceil(rowCount / pagination.pageSize) - 1);
	return { ...pagination, pageIndex: Math.min(pagination.pageIndex, last) };
}

function ResizeHandle<T extends RowData>({ column, width }: { column: Column<Features, T>; width: number }) {
	return (
		<ColumnResizeHandle
			label={columnLabel(column)}
			width={width}
			min={72}
			max={520}
			onWidth={(value) => {
				column.table.setColumnSizing((prev) => ({
					...prev,
					[column.id]: value,
				}));
			}}
		/>
	);
}

const ARIA_SORT = { asc: "ascending", desc: "descending" } as const;

// Header and column objects keep their identity while table state changes, so the
// React Compiler would skip re-rendering a head that read its sort and width itself.
function DataTableHead<T extends RowData>({
	header,
	sorted,
	width,
}: {
	header: Header<Features, T>;
	sorted: false | SortDirection;
	width: number;
}) {
	const { column } = header;
	const meta = column.columnDef.meta;
	const resizable = column.getCanResize();
	const label = (
		<>
			{flexRender(column.columnDef.header, header.getContext())}
			{meta?.soon === true && <SoonTag />}
		</>
	);
	const Arrow = sorted === "desc" ? ArrowDownIcon : ArrowUpIcon;
	return (
		<TableHead
			className={cn(resizable && "relative", meta?.headClassName)}
			style={resizable ? { width } : undefined}
			aria-sort={sorted === false ? undefined : ARIA_SORT[sorted]}
		>
			{column.getCanSort() ? (
				<button
					type="button"
					className={cn(
						"flex items-center gap-1 rounded-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
						sorted !== false && "text-foreground",
					)}
					onClick={column.getToggleSortingHandler()}
				>
					{label}
					<Arrow aria-hidden="true" className={cn("size-3", sorted === false && "invisible")} />
				</button>
			) : (
				<span className="inline-flex items-center gap-2">{label}</span>
			)}
			{resizable && <ResizeHandle column={column} width={width} />}
		</TableHead>
	);
}

export function DataTable<T extends RowData>({
	table,
	empty,
	className,
	style,
}: {
	table: DataTableInstance<T>;
	empty: ReactNode;
	className?: string;
	style?: CSSProperties;
}) {
	const rows = table.getRowModel().rows;
	return (
		<Table className={className} style={style}>
			<TableHeader>
				{table.getHeaderGroups().map((group) => (
					<TableRow key={group.id}>
						{group.headers.map((header) => (
							<DataTableHead
								key={header.id}
								header={header}
								sorted={header.column.getIsSorted()}
								width={header.getSize()}
							/>
						))}
					</TableRow>
				))}
			</TableHeader>
			<TableBody>
				{rows.length === 0 && (
					<TableRow>
						<TableCell colSpan={table.getVisibleLeafColumns().length} className="h-auto p-0 whitespace-normal">
							{empty}
						</TableCell>
					</TableRow>
				)}
				{rows.map((row) => (
					<TableRow key={row.id} data-state={row.getIsSelected() ? "selected" : undefined}>
						{row.getVisibleCells().map((cell) => (
							<TableCell key={cell.id} className={cell.column.columnDef.meta?.cellClassName}>
								{flexRender(cell.column.columnDef.cell, cell.getContext())}
							</TableCell>
						))}
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

export function DataTableColumnsMenu<T extends RowData>({ table }: { table: DataTableInstance<T> }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label="Columns" title="Columns" className="text-fg3" />}
			>
				<Columns3Icon />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[220px]">
				<DropdownMenuGroup>
					<DropdownMenuLabel>Columns</DropdownMenuLabel>
					<Subscribe source={table.atoms.columnVisibility}>
						{(visibility) =>
							table
								.getAllLeafColumns()
								.filter((column) => column.getCanHide())
								.map((column) => (
									<DropdownMenuCheckboxItem
										key={column.id}
										checked={visibility[column.id] !== false}
										onCheckedChange={(on) => {
											column.toggleVisibility(on);
										}}
									>
										<span className="flex-1">{columnLabel(column)}</span>
										{column.columnDef.meta?.soon === true && <SoonTag />}
									</DropdownMenuCheckboxItem>
								))
						}
					</Subscribe>
				</DropdownMenuGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export function DataTablePager<T extends RowData>({
	table,
	pageSizes,
	empty = "0 of 0",
}: {
	table: ReactTable<Features, T>;
	pageSizes: readonly number[];
	empty?: string;
}) {
	const { pageIndex, pageSize } = table.state.pagination;
	const total = table.getRowCount();
	return (
		<Pager
			label={total > 0 ? pageRangeLabel(pageIndex, pageSize, total) : empty}
			page={pageIndex}
			pages={Math.max(1, table.getPageCount())}
			total={total}
			pageSizes={pageSizes}
			pageSize={pageSize}
			onPageSize={(size) => {
				table.setPagination({ pageIndex: 0, pageSize: size });
			}}
			onFirst={table.getCanPreviousPage() ? table.firstPage : null}
			onPrev={table.getCanPreviousPage() ? table.previousPage : null}
			onNext={table.getCanNextPage() ? table.nextPage : null}
			onLast={table.getCanNextPage() ? table.lastPage : null}
		/>
	);
}
