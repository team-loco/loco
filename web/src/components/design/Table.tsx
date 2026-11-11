import {
	Table as TableBase,
	TableBody as TableBodyBase,
	TableCell as TableCellBase,
	TableHead as TableHeadBase,
	TableHeader as TableHeaderBase,
	TableRow as TableRowBase,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

const FLUSH = "[&_td:first-child]:pl-5 [&_td:last-child]:pr-5 [&_th:first-child]:pl-5 [&_th:last-child]:pr-5";

function Table({ className, flush = false, ...props }: React.ComponentProps<typeof TableBase> & { flush?: boolean }) {
	return (
		<div className={cn(!flush && "px-5 pb-1.5")}>
			<TableBase className={cn("text-[13px]", flush && FLUSH, className)} {...props} />
		</div>
	);
}

function TableHeader({ className, ...props }: React.ComponentProps<typeof TableHeaderBase>) {
	return <TableHeaderBase className={cn("[&_tr]:border-line2 [&_tr]:hover:bg-transparent", className)} {...props} />;
}

// Expandable history rows render one tbody per row; the stock last-row rule would drop every separator.
function TableBody({ className, ...props }: React.ComponentProps<typeof TableBodyBase>) {
	return <TableBodyBase className={cn("not-last:[&_tr:last-child]:border-b", className)} {...props} />;
}

function TableRow({ className, ...props }: React.ComponentProps<typeof TableRowBase>) {
	return (
		<TableRowBase
			className={cn("border-line2 hover:bg-transparent data-[state=selected]:bg-panel2", className)}
			{...props}
		/>
	);
}

function TableHead({ className, ...props }: React.ComponentProps<typeof TableHeadBase>) {
	return (
		<TableHeadBase
			className={cn("h-[38px] px-2.5 text-[12.5px] font-normal text-fg3 first:pl-0 last:pr-0", className)}
			{...props}
		/>
	);
}

function TableCell({ className, ...props }: React.ComponentProps<typeof TableCellBase>) {
	return <TableCellBase className={cn("h-[62px] px-2.5 py-3 first:pl-0 last:pr-0", className)} {...props} />;
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow };
