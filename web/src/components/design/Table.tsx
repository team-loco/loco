import {
	Table as TableBase,
	TableBody,
	TableCaption,
	TableCell as TableCellBase,
	TableFooter,
	TableHead as TableHeadBase,
	TableHeader,
	TableRow as TableRowBase,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"

function Table({ className, ...props }: React.ComponentProps<typeof TableBase>) {
	return <TableBase className={cn("text-base", className)} {...props} />
}

function TableRow({ className, ...props }: React.ComponentProps<typeof TableRowBase>) {
	return <TableRowBase className={cn("border-line hover:bg-bg2 data-[state=selected]:bg-bg3", className)} {...props} />
}

function TableHead({ className, ...props }: React.ComponentProps<typeof TableHeadBase>) {
	return (
		<TableHeadBase
			className={cn("h-10 px-4 text-sm font-semibold text-fg2 first:pl-4 last:pr-4", className)}
			{...props}
		/>
	)
}

function TableCell({ className, ...props }: React.ComponentProps<typeof TableCellBase>) {
	return <TableCellBase className={cn("h-11 px-4 py-0 first:pl-4 last:pr-4", className)} {...props} />
}

export { Table, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow }
