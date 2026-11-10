import {
	ArrowDownIcon,
	ArrowUpIcon,
	Columns3Icon,
	CopyIcon,
	ScrollTextIcon,
	SearchXIcon,
	Settings2Icon,
	XIcon,
} from "lucide-react";
import { useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { toast } from "sonner";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { ResourceRowMenu } from "@/components/ResourceRowMenu";
import { Button } from "@/components/design/Button";
import { Checkbox } from "@/components/design/Checkbox";
import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { SoonTag } from "@/components/design/SoonTag";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { pagedRows } from "@/lib/paging";
import { formatCpu, formatMemory } from "@/lib/quantity";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { pageSelection, toggleId, togglePage } from "@/lib/selection";
import { agoLabel, formatMonthDayYear } from "@/lib/time";
import { cn } from "@/lib/utils";

import { BulkActions } from "./BulkActions";
import {
	ALL,
	cellSummary,
	filterRows,
	firstDir,
	linkEnvironment,
	PAGE_SIZES,
	readTableState,
	regionCounts,
	replicasLabel,
	sortRows,
	STATUS_FILTERS,
	statusCounts,
	tableColumns,
	writeTableState,
	type Column,
	type EnvCell,
	type ResourceRow,
	type TableState,
} from "./table";

const MENU_WIDTH = 40;
const CHECK_WIDTH = 36;
const MIN_WIDTH = 72;
const MAX_WIDTH = 520;
const KEY_STEP = 16;

function clampWidth(w: number): number {
	return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(w)));
}

function ResizeHandle({ label, width, onWidth }: { label: string; width: number; onWidth: (w: number) => void }) {
	const dragRef = useRef<{ x: number; width: number } | null>(null);
	const onKeyDown = (e: KeyboardEvent) => {
		if (e.key === "ArrowLeft") onWidth(clampWidth(width - KEY_STEP));
		else if (e.key === "ArrowRight") onWidth(clampWidth(width + KEY_STEP));
		else return;
		e.preventDefault();
	};
	return (
		<div
			role="separator"
			aria-orientation="vertical"
			aria-label={`Resize ${label}`}
			aria-valuenow={width}
			aria-valuemin={MIN_WIDTH}
			aria-valuemax={MAX_WIDTH}
			tabIndex={0}
			onKeyDown={onKeyDown}
			onPointerDown={(e: PointerEvent<HTMLDivElement>) => {
				e.currentTarget.setPointerCapture(e.pointerId);
				dragRef.current = { x: e.clientX, width };
			}}
			onPointerMove={(e) => {
				if (dragRef.current !== null) onWidth(clampWidth(dragRef.current.width + e.clientX - dragRef.current.x));
			}}
			onPointerUp={() => {
				dragRef.current = null;
			}}
			className="absolute top-2 right-0 bottom-2 w-2 cursor-col-resize touch-none rounded-sm outline-none after:absolute after:inset-y-0 after:right-[3px] after:w-px after:bg-line2 hover:after:bg-fg4 focus-visible:after:bg-foreground"
		/>
	);
}

function EnvCellView({ cell, nowMs }: { cell: EnvCell | undefined; nowMs: number }) {
	if (cell === undefined) return <span className="text-fg4">—</span>;
	const style = resourceStatusStyle(cell.status);
	const tip = [style.label, cell.note, cell.deployedMs === 0 ? "" : `deployed ${agoLabel(cell.deployedMs, nowMs)}`]
		.filter((s) => s !== "")
		.join(" · ");
	return (
		<Tooltip>
			<TooltipTrigger
				render={<button type="button" aria-label={`${tip}, ${cellSummary(cell)}`} />}
				className="flex max-w-full min-w-0 flex-col items-start gap-0.5 rounded-sm text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
			>
				<span className="flex min-w-0 items-center gap-2">
					<StatusGlyph glyph={style.glyph} />
					<span className="truncate font-mono text-[12.5px] text-foreground">{cell.tag === "" ? "—" : cell.tag}</span>
				</span>
				<span className="truncate font-mono text-[11.5px] text-fg3">{cellSummary(cell)}</span>
			</TooltipTrigger>
			<TooltipContent>{tip}</TooltipContent>
		</Tooltip>
	);
}

function Mono({ text }: { text: string | null }) {
	if (text === null || text === "") return <span className="text-fg4">—</span>;
	return (
		<span title={text} className="block truncate font-mono text-[12px] text-fg2">
			{text}
		</span>
	);
}

function DataCell({ column, row, nowMs }: { column: Column; row: ResourceRow; nowMs: number }) {
	switch (column.kind) {
		case "name":
			return null;
		case "env":
			return <EnvCellView cell={row.cells.get(column.environment?.id ?? "")} nowMs={nowMs} />;
		case "size":
			return <Mono text={row.cpuMilli === 0 ? null : `${formatCpu(row.cpuMilli)} · ${formatMemory(row.memMi)}`} />;
		case "replicas":
			return <Mono text={replicasLabel(row)} />;
		case "cost":
			return null;
		case "address":
			return <Mono text={row.address} />;
		case "image":
			return <Mono text={row.image} />;
		case "created":
			return <Mono text={row.createdMs === 0 ? null : formatMonthDayYear(row.createdMs)} />;
	}
}

function RowActions({
	row,
	environments,
	active,
	orgId,
	workspaceId,
}: {
	row: ResourceRow;
	environments: Environment[];
	active: Environment | undefined;
	orgId: string;
	workspaceId: string;
}) {
	const navigate = useNavigate();
	const { resource, address } = row;
	const env = linkEnvironment(row, environments, active);
	const envParams: Record<string, string> = env === undefined ? {} : { env: env.name };
	const settings = new URLSearchParams({ tab: "settings", ...envParams });
	return (
		<ResourceRowMenu resource={resource} workspaceId={workspaceId} title={resource.name}>
			{row.cells.size > 0 && (
				<DropdownMenuItem
					onClick={() =>
						void navigate(observabilityPath(orgId, workspaceId, "logs", { ...envParams, r: resource.name }))
					}
				>
					<ScrollTextIcon className="text-fg3" />
					Logs
				</DropdownMenuItem>
			)}
			<DropdownMenuItem
				onClick={() => void navigate(`${resourcePath(orgId, workspaceId, resource.id)}?${settings.toString()}`)}
			>
				<Settings2Icon className="text-fg3" />
				Settings
			</DropdownMenuItem>
			{address !== null && (
				<DropdownMenuItem
					onClick={() => {
						void navigator.clipboard.writeText(address).then(() => toast.success(`Copied ${address}`));
					}}
				>
					<CopyIcon className="text-fg3" />
					Copy address
				</DropdownMenuItem>
			)}
		</ResourceRowMenu>
	);
}

export function ResourceTable({
	rows,
	environments,
	active,
	orgId,
	workspaceId,
	nowMs,
}: {
	rows: ResourceRow[];
	environments: Environment[];
	active: Environment | undefined;
	orgId: string;
	workspaceId: string;
	nowMs: number;
}) {
	const [params, setParams] = useSearchParams();
	const [widths, setWidths] = useState<Record<string, number>>({});
	const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());
	const columns = tableColumns(environments);
	const state = readTableState(params, columns);
	const { filter, sort } = state;
	const shown = columns.filter((c) => c.kind === "name" || state.visible.includes(c.key));

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

	const visible = sortRows(filterRows(rows, filter), sort, columns);
	const paged = pagedRows(
		visible,
		{ page: state.page, size: state.size },
		(next) => {
			update(next);
		},
		PAGE_SIZES,
	);
	const counts = statusCounts(rows, filter);
	const regionOptions = [
		{ value: ALL, label: "All regions", count: filterRows(rows, { ...filter, status: ALL, region: ALL }).length },
		...regionCounts(rows, filter).map((r) => ({ value: r.region, label: r.region, count: r.count })),
	];
	const widthOf = (c: Column) => widths[c.key] ?? c.width;
	const minWidth = shown.reduce((n, c) => n + widthOf(c), MENU_WIDTH + CHECK_WIDTH);
	const pageIds = paged.rows.map((r) => r.resource.id);
	const selection = pageSelection(selected, pageIds);
	const selectedRows = paged.rows.filter((r) => selected.has(r.resource.id));
	const hasFilters = filter.query.trim() !== "" || filter.status !== ALL || filter.region !== ALL;

	const toggleSort = (c: Column) => {
		const dir = sort.key === c.key ? (sort.dir === "asc" ? "desc" : "asc") : firstDir(c);
		update({ sort: { key: c.key, dir }, page: 0 });
	};
	const toggleColumn = (key: string) => {
		const on = state.visible.includes(key);
		update({
			visible: columns
				.filter((c) => c.kind !== "name" && (c.key === key ? !on : state.visible.includes(c.key)))
				.map((c) => c.key),
		});
	};

	const toolbar = (
		<>
			<SearchInput
				value={filter.query}
				placeholder="Search name, image, version"
				aria-label="Search resources"
				className="w-full sm:w-[220px]"
				onChange={(e) => {
					setFilter({ query: e.target.value });
				}}
				onClear={() => {
					setFilter({ query: "" });
				}}
			/>
			<ToggleGroup
				variant="segmented"
				aria-label="Filter by status"
				className="max-w-full overflow-x-auto sm:ml-auto"
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

	const columnsMenu = (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label="Columns" title="Columns" className="text-fg3" />}
			>
				<Columns3Icon />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[220px]">
				<DropdownMenuGroup>
					<DropdownMenuLabel>Columns</DropdownMenuLabel>
					{columns
						.filter((c) => c.kind !== "name")
						.map((c) => (
							<DropdownMenuCheckboxItem
								key={c.key}
								checked={state.visible.includes(c.key)}
								onCheckedChange={() => {
									toggleColumn(c.key);
								}}
							>
								<span className="flex-1">{c.label}</span>
								{c.kind === "cost" && <SoonTag />}
							</DropdownMenuCheckboxItem>
						))}
				</DropdownMenuGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);

	const bulk = (
		<BulkActions
			rows={selectedRows}
			environment={active}
			onClear={() => {
				setSelected(new Set());
			}}
			onDone={setSelected}
		/>
	);

	return (
		<Section actions={selection.some ? bulk : toolbar}>
			<Table className="table-fixed" style={{ minWidth }}>
				<TableHeader>
					<TableRow>
						<TableHead style={{ width: CHECK_WIDTH }}>
							<Checkbox
								aria-label={selection.all ? "Deselect this page" : "Select this page"}
								disabled={pageIds.length === 0}
								checked={selection.all}
								indeterminate={selection.some && !selection.all}
								onCheckedChange={() => {
									setSelected(togglePage(selected, pageIds));
								}}
							/>
						</TableHead>
						{shown.map((c) => {
							const on = sort.key === c.key;
							const label = (
								<>
									{c.label}
									{c.kind === "cost" && <SoonTag />}
								</>
							);
							return (
								<TableHead
									key={c.key}
									className="relative"
									style={c.kind === "name" ? undefined : { width: widthOf(c) }}
									aria-sort={on ? (sort.dir === "asc" ? "ascending" : "descending") : undefined}
								>
									{c.sortable ? (
										<button
											type="button"
											className={cn("flex items-center gap-1 hover:text-foreground", on && "text-foreground")}
											onClick={() => {
												toggleSort(c);
											}}
										>
											{label}
											{on &&
												(sort.dir === "asc" ? (
													<ArrowUpIcon className="size-3" />
												) : (
													<ArrowDownIcon className="size-3" />
												))}
										</button>
									) : (
										<span className="flex items-center gap-2">{label}</span>
									)}
									{c.kind !== "name" && (
										<ResizeHandle
											label={c.label}
											width={widthOf(c)}
											onWidth={(w) => {
												setWidths({ ...widths, [c.key]: w });
											}}
										/>
									)}
								</TableHead>
							);
						})}
						<TableHead className="text-right" style={{ width: MENU_WIDTH }}>
							{columnsMenu}
						</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{paged.rows.length === 0 && (
						<TableRow>
							<TableCell colSpan={shown.length + 2} className="h-auto p-0 whitespace-normal">
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
							</TableCell>
						</TableRow>
					)}
					{paged.rows.map((row) => (
						<TableRow key={row.resource.id} data-state={selected.has(row.resource.id) ? "selected" : undefined}>
							<TableCell>
								<Checkbox
									aria-label={`Select ${row.resource.name}`}
									checked={selected.has(row.resource.id)}
									onCheckedChange={() => {
										setSelected(toggleId(selected, row.resource.id));
									}}
								/>
							</TableCell>
							{shown.map((c) =>
								c.kind === "name" ? (
									<TableCell key={c.key}>
										<div className="flex min-w-0 flex-col gap-0.5">
											<Link
												to={resourcePath(orgId, workspaceId, row.resource.id)}
												className="truncate text-[14.5px] font-medium text-foreground underline-offset-3 hover:underline"
											>
												{row.resource.name}
											</Link>
											<span className="truncate text-[12.5px] text-fg3">{row.resource.description ?? ""}</span>
										</div>
									</TableCell>
								) : (
									<TableCell key={c.key}>
										<DataCell column={c} row={row} nowMs={nowMs} />
									</TableCell>
								),
							)}
							<TableCell className="text-right">
								<RowActions
									row={row}
									environments={environments}
									active={active}
									orgId={orgId}
									workspaceId={workspaceId}
								/>
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
			<Pager {...paged.pager} {...paged.ends} />
		</Section>
	);
}
