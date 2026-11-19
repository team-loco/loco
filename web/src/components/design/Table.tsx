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

// Marks the scroll container with how far it has scrolled so the stylesheet can show that more columns exist.
export function trackScroll(container: HTMLElement | null) {
	if (container === null) return;
	const update = () => {
		const max = container.scrollWidth - container.clientWidth;
		if (max <= 1) container.removeAttribute("data-scroll");
		else
			container.setAttribute(
				"data-scroll",
				container.scrollLeft >= max - 1 ? "end" : container.scrollLeft > 0 ? "middle" : "start",
			);
	};
	update();
	const observer = new ResizeObserver(update);
	observer.observe(container);
	if (container.firstElementChild !== null) observer.observe(container.firstElementChild);
	container.addEventListener("scroll", update, { passive: true });
	return () => {
		observer.disconnect();
		container.removeEventListener("scroll", update);
	};
}

const trackTable = (table: HTMLTableElement | null) => trackScroll(table?.parentElement ?? null);

/** `inset` keeps rows and their dividers 20px inside the card instead of running edge to edge. */
function Table({ className, inset = false, ...props }: React.ComponentProps<typeof TableBase> & { inset?: boolean }) {
	const table = <TableBase ref={trackTable} className={cn("text-[13px]", !inset && FLUSH, className)} {...props} />;
	return inset ? <div className="px-5">{table}</div> : table;
}

function TableHeader({ className, ...props }: React.ComponentProps<typeof TableHeaderBase>) {
	return <TableHeaderBase className={cn("[&_tr]:border-line2 [&_tr]:hover:bg-transparent", className)} {...props} />;
}

// Expandable history rows render one tbody per row; the stock last-row rule would drop every separator.
function TableBody({ className, ...props }: React.ComponentProps<typeof TableBodyBase>) {
	return <TableBodyBase className={cn("not-last:[&_tr:last-child]:border-b", className)} {...props} />;
}

function TableRow({
	className,
	interactive = false,
	...props
}: React.ComponentProps<typeof TableRowBase> & { interactive?: boolean }) {
	const clickable = interactive || props.onClick !== undefined;
	return (
		<TableRowBase
			data-interactive={clickable || undefined}
			className={cn(
				"group/row border-line2 hover:bg-transparent data-[state=selected]:bg-panel2 [&:has(+_[data-subheader])]:border-b-0",
				"has-data-row-link:relative has-data-row-link:hover:bg-(--row-hover) has-data-row-link:[&_:is(button,[tabindex])]:relative has-data-row-link:[&_:is(button,[tabindex])]:z-1",
				clickable && "hover:bg-(--row-hover)",
				className,
			)}
			{...props}
		/>
	);
}

function TableHead({ className, ...props }: React.ComponentProps<typeof TableHeadBase>) {
	return (
		<TableHeadBase
			className={cn("h-10 px-2.5 text-[12.5px] font-normal text-fg3 first:pl-0 last:pr-0", className)}
			{...props}
		/>
	);
}

function TableCell({ className, ...props }: React.ComponentProps<typeof TableCellBase>) {
	return <TableCellBase className={cn("h-(--row-h,52px) px-2.5 py-2.5 first:pl-0 last:pr-0", className)} {...props} />;
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow };
