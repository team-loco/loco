import {
	columnResizingFeature,
	columnSizingFeature,
	columnVisibilityFeature,
	createPaginatedRowModel,
	createSortedRowModel,
	functionalUpdate,
	metaHelper,
	rowPaginationFeature,
	rowSortingFeature,
	sortFn_basic,
	Subscribe,
	tableFeatures,
	useTable,
	type Column,
	type ColumnDef,
	type ColumnDefTemplate,
	type ColumnSizingState,
	type Header,
	type PaginationState,
	type ReactTable,
	type Row,
	type RowData,
	type SortDirection,
	type Table as TanStackTable,
	type TableOptions,
} from "@tanstack/react-table";
import { NavArrow } from "@/components/design/NavArrow";
import { ArrowDownIcon, ArrowUpIcon, ChevronRightIcon, Columns3Icon } from "lucide-react";
import {
	Fragment,
	useRef,
	useState,
	type CSSProperties,
	type KeyboardEvent,
	type MouseEvent,
	type ReactElement,
	type ReactNode,
} from "react";

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
import { Skeleton } from "@/components/design/Skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { useIsMobile } from "@/hooks/use-mobile";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { pageRangeLabel } from "@/lib/paging";
import { useStoredJSON } from "@/lib/storage";
import { cn } from "@/lib/utils";

export interface DataColumnMeta {
	/** Aligns the header and cells to the column's end edge. */
	align?: "end";
	/** Sizes the column to its widest cell, between `minSize` and `fitMax` (or `maxSize`), until the viewer resizes it. */
	fit?: boolean;
	/** Caps a `fit` column's automatic width below the `maxSize` the viewer can resize it to. */
	fitMax?: number;
	/** Gives the column the width left over once every column has its size, until the viewer resizes it; the last column takes it otherwise. */
	grow?: boolean;
	/** Holds the row's name; a clickable row shows its arrow after this column's content, the first column by default. */
	primary?: boolean;
	/** Keeps the column at the right edge while the table scrolls sideways, end-aligned and as wide as its content; the first column always stays at the left. */
	pinEnd?: boolean;
	/** Hides the column below 768px. */
	hideOnMobile?: boolean;
	/** Names the column when its header is not plain text. */
	label?: string;
	cellClassName?: string;
	defaultHidden?: boolean;
}

export const dataTableFeatures = tableFeatures({
	columnVisibilityFeature,
	columnSizingFeature,
	columnResizingFeature,
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

const MIN_WIDTH = 72;
const MAX_WIDTH = 520;

export function columnLabel<T extends RowData>(column: Column<Features, T>): string {
	const header = column.columnDef.header;
	return typeof header === "string" ? header : (column.columnDef.meta?.label ?? column.id);
}

export function pageWithin(pagination: PaginationState, rowCount: number): PaginationState {
	const last = Math.max(0, Math.ceil(rowCount / pagination.pageSize) - 1);
	return { ...pagination, pageIndex: Math.min(pagination.pageIndex, last) };
}

function parseSizing(value: unknown): ColumnSizingState {
	if (typeof value !== "object" || value === null) return {};
	return Object.fromEntries(
		Object.entries(value).filter((entry): entry is [string, number] => Number.isFinite(entry[1])),
	);
}

/** Renders a column's header or cell template in one component type, so columns rebuilt on each render keep their state, such as an open menu. */
function Template<P extends object>({
	render,
	context,
}: {
	render: ColumnDefTemplate<P> | undefined;
	context: P;
}): ReactNode {
	return typeof render === "function" ? (render(context) as ReactNode) : render;
}

/** A table of every row it is given unless the caller paginates; the viewer resizes its columns and this browser keeps the widths under `id`, unless the caller keeps them. */
export function useDataTable<T extends RowData>(
	id: string,
	options: Omit<TableOptions<Features, T>, "features" | "columns"> & { columns: DataColumn<T>[] },
): ReactTable<Features, T> {
	const external = options.onColumnSizingChange !== undefined;
	const [stored, setStored] = useStoredJSON(external ? null : `loco.table-widths.${id}`, parseSizing);
	const mobile = useIsMobile();
	const mobileHidden = mobile
		? options.columns.filter((c) => c.meta?.hideOnMobile === true).map((c): [string, boolean] => [c.id, false])
		: [];
	return useTable({
		...sortOptions,
		manualPagination: true,
		...options,
		features: dataTableFeatures,
		state: {
			...options.state,
			columnVisibility: { ...options.state?.columnVisibility, ...Object.fromEntries(mobileHidden) },
			columnSizing: external ? (options.state?.columnSizing ?? {}) : stored,
		},
		onColumnSizingChange:
			options.onColumnSizingChange ??
			((updater) => {
				setStored((current) => functionalUpdate(updater, current));
			}),
	});
}

/** One grid track per visible column: resized and fixed columns take their width, `fit` columns their widest cell. */
function growIndexOf<T extends RowData>(columns: Column<Features, T>[], sizing: ColumnSizingState): number {
	const grow = columns.findIndex((c) => c.columnDef.meta?.grow === true && !(c.id in sizing));
	return grow === -1 ? columns.length - 1 : grow;
}

/** The column's width in `sizing`, which may hold a drag in progress, within its bounds; its default size otherwise. */
function widthOf<T extends RowData>(column: Column<Features, T>, sizing: ColumnSizingState): number {
	const width = sizing[column.id];
	if (width === undefined) return column.getSize();
	return Math.min(maxOf(column), Math.max(column.columnDef.minSize ?? MIN_WIDTH, width));
}

/** A `grow` column has no default cap, so a drag can start from any width it fills. */
function maxOf<T extends RowData>(column: Column<Features, T>): number {
	return column.columnDef.maxSize ?? (column.columnDef.meta?.grow === true ? Infinity : MAX_WIDTH);
}

export function gridTracks<T extends RowData>(columns: Column<Features, T>[], sizing: ColumnSizingState): string {
	const growIndex = growIndexOf(columns, sizing);
	return columns
		.map((column, index) => {
			const size = widthOf(column, sizing);
			const resized = column.id in sizing;
			const fit = column.columnDef.meta?.fit === true && !resized;
			if (column.columnDef.meta?.pinEnd === true)
				return index === growIndex ? "minmax(max-content,1fr)" : "max-content";
			if (index === growIndex) return `minmax(${String(fit ? (column.columnDef.minSize ?? MIN_WIDTH) : size)}px,1fr)`;
			if (fit) return "max-content";
			return `${String(size)}px`;
		})
		.join(" ");
}

// A fit column's cells cap the width they ask for, so its track spans the widest cell up to `maxSize`;
// the pinned first column also stops at half the table's width.
function fitBounds<T extends RowData>(
	column: Column<Features, T>,
	sizing: ColumnSizingState,
	index: number,
): CSSProperties | undefined {
	if (column.columnDef.meta?.fit !== true || column.id in sizing) return undefined;
	const max = `${String(column.columnDef.meta.fitMax ?? column.columnDef.maxSize ?? MAX_WIDTH)}px`;
	return { minWidth: column.columnDef.minSize ?? MIN_WIDTH, maxWidth: index === 0 ? `min(${max}, 50cqi)` : max };
}

// Marks the scroll container with its scroll position and the pinned end column's width, which the stylesheet uses to fade the clipped edge and shade pinned columns.
function trackScroll(container: HTMLElement | null) {
	if (container === null) return;
	const update = () => {
		const end = container.querySelector<HTMLElement>("thead [data-pin=end]");
		container.style.setProperty("--pin-end", `${String(end?.offsetWidth ?? 0)}px`);
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
	const head = container.querySelector("[data-slot=table-header] > tr");
	if (head !== null) observer.observe(head);
	container.addEventListener("scroll", update, { passive: true });
	return () => {
		observer.disconnect();
		container.removeEventListener("scroll", update);
	};
}

const SPAN = <span />;

/** Text that truncates to its cell and shows the full value in a tooltip when it does not fit. */
export function CellText({
	text,
	detail,
	className,
	render = SPAN,
}: {
	text: string;
	/** Secondary text shown muted after `text`. */
	detail?: string | undefined;
	className?: string;
	render?: ReactElement;
}) {
	const full = detail === undefined ? text : `${text} · ${detail}`;
	const ref = useRef<HTMLElement>(null);
	const [open, setOpen] = useState(false);
	return (
		<Tooltip
			open={open}
			onOpenChange={(next) => {
				const el = ref.current;
				setOpen(next && el !== null && el.scrollWidth > el.clientWidth);
			}}
		>
			<TooltipTrigger
				ref={(el) => {
					ref.current = el;
				}}
				render={render}
				className={cn("block truncate", className)}
			>
				{text}
				{detail !== undefined && <span className="text-fg3"> · {detail}</span>}
			</TooltipTrigger>
			<TooltipContent className="max-w-[min(480px,90vw)] break-all">{full}</TooltipContent>
		</Tooltip>
	);
}

const ARIA_SORT = { asc: "ascending", desc: "descending" } as const;

const PINNED = "sticky z-1 bg-(--card-bg,var(--panel)) data-[pin=end]:right-0 data-[pin=start]:left-0";

function alignOf<T extends RowData>(column: Column<Features, T>): "end" | undefined {
	const meta = column.columnDef.meta;
	return meta?.pinEnd === true ? "end" : meta?.align;
}

function pinOf<T extends RowData>(column: Column<Features, T>, index: number): "start" | "end" | undefined {
	if (index === 0) return "start";
	return column.columnDef.meta?.pinEnd === true ? "end" : undefined;
}

// Header and column objects keep their identity while table state changes, so the
// React Compiler would skip re-rendering a head that read its sort and width itself.
function DataTableHead<T extends RowData>({
	header,
	sorted,
	width,
	pin,
	bounds,
	grow,
	onPreview,
}: {
	header: Header<Features, T>;
	sorted: false | SortDirection;
	width: number;
	pin: "start" | "end" | undefined;
	bounds: CSSProperties | undefined;
	grow: boolean;
	onPreview: (width: number | null) => void;
}) {
	const { column } = header;
	const align = alignOf(column);
	const label = <Template render={column.columnDef.header} context={header.getContext()} />;
	const Arrow = sorted === "desc" ? ArrowDownIcon : ArrowUpIcon;
	return (
		<TableHead
			role="columnheader"
			aria-label={typeof column.columnDef.header === "string" ? column.columnDef.header : column.columnDef.meta?.label}
			data-pin={pin}
			data-grow={grow || undefined}
			data-align={align}
			className={cn(
				"group/head relative flex h-10 min-w-0 items-center overflow-hidden px-2.5 text-[12.5px] font-normal text-fg3",
				align === "end" && "justify-end",
				pin !== undefined && PINNED,
			)}
			style={bounds}
			aria-sort={sorted === false ? undefined : ARIA_SORT[sorted]}
		>
			{column.getCanSort() ? (
				<button
					type="button"
					className={cn(
						"group/sort flex min-w-0 items-center rounded-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
						align === "end" && "flex-row-reverse",
					)}
					onClick={column.getToggleSortingHandler()}
				>
					<span className="truncate">{label}</span>
					{/* A zero-width slot: a header needs no more room than its label. */}
					<span aria-hidden="true" className="relative flex w-0 items-center self-stretch">
						<Arrow
							className={cn(
								"absolute size-3 group-hover/sort:visible group-focus-visible/sort:visible",
								sorted === false && "invisible",
								align === "end" ? "right-1" : "left-1",
							)}
						/>
					</span>
				</button>
			) : (
				<span className="inline-flex min-w-0 items-center gap-2 truncate">{label}</span>
			)}
			{column.getCanResize() && pin !== "end" && (
				<ColumnResizeHandle
					label={columnLabel(column)}
					width={width}
					min={column.columnDef.minSize ?? MIN_WIDTH}
					max={maxOf(column)}
					onWidth={(value) => {
						column.table.setColumnSizing((prev) => ({
							...prev,
							[column.id]: value,
						}));
					}}
					onDrag={onPreview}
				/>
			)}
		</TableHead>
	);
}

// Events from a control in the row, or from portalled content such as a row menu or its dialogs, are not row clicks.
function fromControl(event: MouseEvent<HTMLElement> | KeyboardEvent<HTMLElement>): boolean {
	const target = event.target;
	if (!(target instanceof Node) || !event.currentTarget.contains(target)) return true;
	return (
		target instanceof Element &&
		target !== event.currentTarget &&
		target.closest("a, button, input, label, [role=checkbox], [role=menuitem]") !== null
	);
}

const ROW = "col-span-full grid grid-cols-subgrid border-b border-line2";
const SKELETON_ROWS = [0, 1, 2, 3, 4];

export function DataTable<T extends RowData>({
	table,
	empty = null,
	loading = false,
	activeRowId,
	onRowClick,
	onRowHover,
	rowAction = "open",
	rowLabel,
	rowClassName,
	groupBy,
	renderExpanded,
	className,
	label,
	inset = false,
}: {
	table: ReactTable<Features, T>;
	/** Content of the single row shown when there are no rows; null shows none. */
	empty?: ReactNode;
	loading?: boolean;
	/** The row whose details are open; it shows as selected. */
	activeRowId?: string | null;
	onRowClick?: (row: Row<Features, T>) => void;
	/** Called with the row the pointer enters, and with null when it leaves. */
	onRowHover?: (row: Row<Features, T> | null) => void;
	/** What a row click does: "navigate" to another page shows an arrow on the row, "open" a drawer here does not. */
	rowAction?: "navigate" | "open";
	/** Names a clickable row for assistive technology. */
	rowLabel?: (row: Row<Features, T>) => string;
	rowClassName?: (row: Row<Features, T>) => string | false | undefined;
	/** Starts a labelled group, such as a day, wherever this value changes from the row before; undefined rows sit in no group. */
	groupBy?: (row: T) => string | undefined;
	/** Content shown in a full-width row below the row, such as its expanded details; null shows none. */
	renderExpanded?: (row: Row<Features, T>) => ReactNode;
	className?: string;
	label?: string;
	/** Keeps row content 10px inside the card edge instead of 20px; row tints and dividers reach 10px past it. */
	inset?: boolean;
}) {
	const columns = table.getVisibleLeafColumns();
	const [preview, setPreview] = useState<{ id: string; width: number } | null>(null);
	const sizing =
		preview === null ? table.state.columnSizing : { ...table.state.columnSizing, [preview.id]: preview.width };
	const rows = table.getRowModel().rows;
	const growIndex = growIndexOf(columns, sizing);
	const primaryIndex = Math.max(
		0,
		columns.findIndex((c) => c.columnDef.meta?.primary === true),
	);
	const scroller = (
		<div ref={trackScroll} data-slot="data-table" className="@container relative w-full overflow-x-auto [--row-h:52px]">
			<table
				data-slot="table"
				aria-label={label}
				className={cn(
					"grid w-full text-[13px] [&_td]:px-2.5 [&_th]:px-2.5",
					inset
						? "[&_tr>:first-child]:pl-2.5 [&_tr>:last-child]:pr-2.5"
						: "[&_tr>:first-child]:pl-5 [&_tr>:last-child]:pr-5",
					className,
				)}
				style={{ gridTemplateColumns: gridTracks(columns, sizing) }}
			>
				<TableHeader role="rowgroup" className="col-span-full grid grid-cols-subgrid [&_tr]:border-line2">
					{table.getHeaderGroups().map((group) => (
						<TableRow role="row" key={group.id} className={cn(ROW, "hover:bg-transparent")}>
							{group.headers.map((header, index) => (
								<DataTableHead
									key={header.id}
									header={header}
									sorted={header.column.getIsSorted()}
									width={widthOf(header.column, sizing)}
									pin={pinOf(header.column, index)}
									bounds={fitBounds(header.column, sizing, index)}
									grow={index === growIndex}
									onPreview={(width) => {
										setPreview(width === null ? null : { id: header.column.id, width });
									}}
								/>
							))}
						</TableRow>
					))}
				</TableHeader>
				<TableBody role="rowgroup" className="col-span-full grid grid-cols-subgrid [&_tr:last-child]:border-0">
					{loading &&
						SKELETON_ROWS.map((i) => (
							<TableRow role="row" key={i} className={cn(ROW, "hover:bg-transparent")}>
								<TableCell role="cell" className="col-span-full flex h-(--row-h) items-center">
									<Skeleton className="h-3 w-full" />
								</TableCell>
							</TableRow>
						))}
					{!loading && rows.length === 0 && empty !== null && (
						<TableRow role="row" className={cn(ROW, "hover:bg-transparent")}>
							<TableCell role="cell" className="col-span-full p-0 whitespace-normal">
								{empty}
							</TableCell>
						</TableRow>
					)}
					{!loading &&
						rows.map((row, i) => {
							const selected = row.id === activeRowId;
							const group = groupBy?.(row.original);
							const previous = rows[i - 1];
							const opensGroup =
								group !== undefined && (previous === undefined || groupBy?.(previous.original) !== group);
							const expanded = renderExpanded?.(row) ?? null;
							return (
								<Fragment key={row.id}>
									{opensGroup && (
										<TableRow role="row" data-group="" className={cn(ROW, "border-b-0 hover:bg-transparent")}>
											<TableCell role="cell" className="col-span-full pt-4 pb-1.5 text-[12px] text-fg3">
												<span className="sticky left-5">{group}</span>
											</TableCell>
										</TableRow>
									)}
									<TableRow
										role="row"
										data-state={selected ? "selected" : undefined}
										data-interactive={onRowClick === undefined ? undefined : ""}
										aria-selected={activeRowId === undefined ? undefined : row.id === activeRowId}
										aria-label={rowLabel?.(row)}
										tabIndex={onRowClick === undefined ? undefined : 0}
										onClick={
											onRowClick === undefined
												? undefined
												: (event) => {
														if (!fromControl(event)) onRowClick(row);
													}
										}
										onMouseEnter={
											onRowHover === undefined
												? undefined
												: () => {
														onRowHover(row);
													}
										}
										onMouseLeave={
											onRowHover === undefined
												? undefined
												: () => {
														onRowHover(null);
													}
										}
										onKeyDown={
											onRowClick === undefined
												? undefined
												: (event) => {
														if (event.key !== "Enter" || fromControl(event)) return;
														event.preventDefault();
														onRowClick(row);
													}
										}
										className={cn(
											ROW,
											"group/row group/nav relative outline-none has-[+[data-group]]:border-b-0",
											expanded !== null && "border-b-0",
											rowClassName?.(row),
										)}
									>
										{row.getVisibleCells().map((cell, index) => {
											const pin = pinOf(cell.column, index);
											return (
												<TableCell
													role="cell"
													key={cell.id}
													data-pin={pin}
													style={fitBounds(cell.column, sizing, index)}
													className={cn(
														"flex min-h-(--row-h) min-w-0 items-center overflow-hidden py-2.5",
														alignOf(cell.column) === "end" && "justify-end",
														pin !== undefined && PINNED,
														cell.column.columnDef.meta?.cellClassName,
													)}
												>
													<Template render={cell.column.columnDef.cell} context={cell.getContext()} />
													{onRowClick !== undefined && rowAction === "navigate" && index === primaryIndex && (
														<NavArrow />
													)}
												</TableCell>
											);
										})}
									</TableRow>
									{expanded !== null && (
										<TableRow role="row" className={cn(ROW, "hover:bg-transparent")}>
											{/* Zero width keeps the details out of the column sizing; min-w-full then gives them the row. */}
											<TableCell role="cell" className="col-span-full w-0 min-w-full p-0 whitespace-normal">
												{expanded}
											</TableCell>
										</TableRow>
									)}
								</Fragment>
							);
						})}
				</TableBody>
			</table>
		</div>
	);
	return inset ? <div className="px-2.5">{scroller}</div> : scroller;
}

/** A chevron column that shows or hides the details of row `openId`; `name` completes "Show …" and "Hide …". */
export function expandColumn<T extends RowData>(
	openId: string | null,
	onOpen: (id: string | null) => void,
	name: (row: T) => string,
): DataColumn<T> {
	return {
		id: "toggle",
		size: 40,
		minSize: 40,
		enableResizing: false,
		meta: { label: "Details", fit: true },
		cell: ({ row }) => {
			const expanded = openId === row.id;
			return (
				<Button
					variant="ghost"
					size="icon-xs"
					aria-expanded={expanded}
					aria-label={`${expanded ? "Hide" : "Show"} ${name(row.original)}`}
					onClick={() => {
						onOpen(expanded ? null : row.id);
					}}
				>
					<ChevronRightIcon className={cn("transition-transform", expanded && "rotate-90")} />
				</Button>
			);
		},
	};
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
			total={total}
			pageSizes={pageSizes}
			pageSize={pageSize}
			onPageSize={(size) => {
				table.setPagination({ pageIndex: 0, pageSize: size });
			}}
			onFirst={table.getCanPreviousPage() ? table.firstPage : null}
			onPrev={table.getCanPreviousPage() ? table.previousPage : null}
			onNext={table.getCanNextPage() ? table.nextPage : null}
		/>
	);
}
