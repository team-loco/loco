import { useTable, functionalUpdate } from "@tanstack/react-table";
import { sortingFeatures, SortableTableHead } from "@/components/design/DataTable";
import { Columns3Icon, CopyIcon, ScrollTextIcon, SearchXIcon, Settings2Icon, XIcon } from "lucide-react";
import { useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
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
import { DRAFT_NOTE } from "@/lib/drafts";
import { imageVersion } from "@/lib/image";
import { pagedRows } from "@/lib/paging";
import { formatCpu, formatMemory } from "@/lib/quantity";
import { draftSearch, observabilityPath, resourcePath } from "@/lib/routes";
import { pageSelection, toggleId, togglePage } from "@/lib/selection";
import { agoLabel, formatMonthDayYear } from "@/lib/time";
import { cn } from "@/lib/utils";

import { BulkActions } from "./BulkActions";
import {
	ALL,
	cellSummary,
	filterRows,
	isDraftRow,
	linkEnvironment,
	PAGE_SIZES,
	readTableState,
	regionCounts,
	replicasLabel,
	resourceColumns,
	resourceSorting,
	STATUS_FILTERS,
	statusCounts,
	tableColumns,
	writeTableState,
	type Column,
	type EnvCell,
	type DraftRow,
	type ResourceRow,
	type ListRow,
	type TableState,
} from "./table";

const MENU_WIDTH = 40;
const DRAFT_NAME = "underline decoration-fg4 decoration-dashed hover:decoration-foreground";
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

function DraftCellView({ row }: { row: DraftRow }) {
	return (
		<span className="flex max-w-full min-w-0 flex-col items-start gap-0.5">
			<span className="flex min-w-0 items-center gap-2">
				<StatusGlyph glyph="draft" />
				<span className="truncate font-mono text-[12.5px] text-foreground">Draft</span>
			</span>
			<span className="max-w-full truncate font-mono text-[11.5px] text-fg3">{imageVersion(row.draft.image)}</span>
		</span>
	);
}

function NameCell({
	to,
	name,
	description,
	draft = false,
}: {
	to: string;
	name: string;
	description: string;
	draft?: boolean;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<Link
				to={to}
				className={cn(
					"truncate text-[14.5px] font-medium text-foreground underline-offset-3 hover:underline",
					draft && DRAFT_NAME,
				)}
			>
				{name}
			</Link>
			<span className="truncate text-[12.5px] text-fg3">{description}</span>
		</div>
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

function DataCell({ column, row, nowMs }: { column: Column; row: ListRow; nowMs: number }) {
	switch (column.kind) {
		case "name":
			return null;
		case "env":
			if (isDraftRow(row) && row.environmentId === column.environment?.id) return <DraftCellView row={row} />;
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
	rows: ListRow[];
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

	const sorting = resourceSorting(sort);
	const table = useTable({
		features: sortingFeatures,
		columns: resourceColumns(columns),
		data: filterRows(rows, filter),
		state: { sorting },
		enableSortingRemoval: false,
		enableMultiSort: false,
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next !== undefined) update({ sort: { key: next.id, dir: next.desc ? "desc" : "asc" }, page: 0 });
		},
	});
	const visible = table.getRowModel().rows.map((row) => row.original);
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
	const pageResources = paged.rows.filter((r): r is ResourceRow => !isDraftRow(r));
	const pageIds = pageResources.map((r) => r.resource.id);
	const selection = pageSelection(selected, pageIds);
	const selectedRows = pageResources.filter((r) => selected.has(r.resource.id));
	const hasFilters = filter.query.trim() !== "" || filter.status !== ALL || filter.region !== ALL;

	const toggleColumn = (key: string) => {
		const on = state.visible.includes(key);
		update({
			visible: columns
				.filter((c) => c.kind !== "name" && (c.key === key ? !on : state.visible.includes(c.key)))
				.map((c) => c.key),
		});
	};

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
		<Section filters={selection.some ? bulk : filters} search={selection.some ? undefined : search}>
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
							const label = (
								<>
									{c.label}
									{c.kind === "cost" && <SoonTag />}
								</>
							);
							return (
								<SortableTableHead
									column={table.getColumn(c.key)}
									key={c.key}
									className="relative"
									style={c.kind === "name" ? undefined : { width: widthOf(c) }}
								>
									{label}
									{c.kind !== "name" && (
										<ResizeHandle
											label={c.label}
											width={widthOf(c)}
											onWidth={(w) => {
												setWidths({ ...widths, [c.key]: w });
											}}
										/>
									)}
								</SortableTableHead>
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
					{paged.rows.map((row) => {
						const cells = (name: ReactNode) =>
							shown.map((c) => (
								<TableCell key={c.key}>
									{c.kind === "name" ? name : <DataCell column={c} row={row} nowMs={nowMs} />}
								</TableCell>
							));
						if (isDraftRow(row)) {
							return (
								<TableRow key={row.draft.id}>
									<TableCell />
									{cells(
										<NameCell
											to={draftSearch(params, row.draft.id)}
											name={row.draft.name}
											description={DRAFT_NOTE}
											draft
										/>,
									)}
									<TableCell />
								</TableRow>
							);
						}
						const on = selected.has(row.resource.id);
						return (
							<TableRow key={row.resource.id} data-state={on ? "selected" : undefined}>
								<TableCell>
									<Checkbox
										aria-label={`Select ${row.resource.name}`}
										checked={on}
										onCheckedChange={() => {
											setSelected(toggleId(selected, row.resource.id));
										}}
									/>
								</TableCell>
								{cells(
									<NameCell
										to={resourcePath(orgId, workspaceId, row.resource.id)}
										name={row.resource.name}
										description={row.resource.description ?? ""}
									/>,
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
						);
					})}
				</TableBody>
			</Table>
			<Pager {...paged.pager} {...paged.ends} />
		</Section>
	);
}
