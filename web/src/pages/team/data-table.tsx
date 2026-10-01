import {
	type ColumnDef,
	type RowData,
	columnVisibilityFeature,
	flexRender,
	tableFeatures,
	useTable,
} from "@tanstack/react-table";

import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/design/Table";
import Loader from "@/assets/loader.svg?react";

export const features = tableFeatures({ columnVisibilityFeature });

interface DataTableProps<TData extends RowData> {
	columns: ColumnDef<typeof features, TData>[];
	data: TData[];
	isLoading?: boolean;
}

export function DataTable<TData extends RowData>({
	columns,
	data,
	isLoading,
}: DataTableProps<TData>) {
	const table = useTable({
		features,
		data,
		columns,
	});

	if (isLoading) {
		return (
			<div className="flex items-center justify-center py-12">
				<div className="text-center">
					<div className="flex flex-col gap-2 items-center">
						<Loader className="w-6 h-6" />
						<p className="text-foreground">Loading members...</p>
					</div>
				</div>
			</div>
		);
	}

	return (
		<div className="overflow-hidden rounded-lg border">
			<Table>
				<TableHeader>
					{table.getHeaderGroups().map((headerGroup) => (
						<TableRow key={headerGroup.id}>
							{headerGroup.headers.map((header) => {
								return (
									<TableHead key={header.id}>
										{header.isPlaceholder
											? null
											: flexRender(
													header.column.columnDef.header,
													header.getContext()
											  )}
									</TableHead>
								);
							})}
						</TableRow>
					))}
				</TableHeader>
				<TableBody>
					{table.getRowModel().rows.length ? (
						table.getRowModel().rows.map((row) => (
							<TableRow key={row.id}>
								{row.getVisibleCells().map((cell) => (
									<TableCell key={cell.id}>
										{flexRender(cell.column.columnDef.cell, cell.getContext())}
									</TableCell>
								))}
							</TableRow>
						))
					) : (
						<TableRow>
							<TableCell colSpan={columns.length} className="h-24 text-center">
								No members found.
							</TableCell>
						</TableRow>
					)}
				</TableBody>
			</Table>
		</div>
	);
}
