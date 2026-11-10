import {
	createSortedRowModel,
	rowSortingFeature,
	sortFn_basic,
	tableFeatures,
	type Column,
	type ColumnDef,
	type RowData,
	type SortDirection,
} from "@tanstack/react-table";
import { ArrowDownIcon, ArrowUpIcon } from "lucide-react";
import { TableHead } from "@/components/design/Table";
import { cn } from "@/lib/utils";

export const sortingFeatures = tableFeatures({
	rowSortingFeature,
	sortedRowModel: createSortedRowModel(),
	sortFns: { basic: sortFn_basic },
});
export type SortColumn<T extends RowData> = ColumnDef<typeof sortingFeatures, T>;

export function SortableTableHead<T extends RowData>({
	column,
	direction,
	className,
	children,
	afterLabel,
	...props
}: Omit<React.ComponentProps<typeof TableHead>, "children"> & {
	column: Column<typeof sortingFeatures, T> | undefined;
	direction: SortDirection | false;
	children: React.ReactNode;
	afterLabel?: React.ReactNode;
}) {
	if (column === undefined) throw new Error("Unknown table column");
	return (
		<TableHead
			className={className}
			aria-sort={direction === false ? undefined : direction === "asc" ? "ascending" : "descending"}
			{...props}
		>
			{column.getCanSort() ? (
				<button
					type="button"
					className={cn(
						"flex items-center gap-1 rounded-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
						direction !== false && "text-foreground",
					)}
					onClick={column.getToggleSortingHandler()}
				>
					{children}
					{direction === "asc" ? (
						<ArrowUpIcon className="size-3" />
					) : direction === "desc" ? (
						<ArrowDownIcon className="size-3" />
					) : null}
				</button>
			) : (
				children
			)}
			{afterLabel}
		</TableHead>
	);
}
