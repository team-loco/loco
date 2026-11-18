import { useTimePreferences } from "@/lib/use-time-preferences";
import { Subscribe } from "@tanstack/react-table";
import { CopyIcon, RocketIcon, ScrollTextIcon, Settings2Icon } from "lucide-react";
import { createContext, use } from "react";
import { useLocation, useNavigate } from "react-router";
import { toast } from "sonner";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { ResourceRowMenu } from "@/components/ResourceRowMenu";
import { Checkbox } from "@/components/design/Checkbox";
import { DataTableColumnsMenu, type DataColumn } from "@/components/design/DataTable";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { RowLink } from "@/components/design/RowLink";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { type Glyph, StatusGlyph } from "@/components/design/StatusGlyph";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { DRAFT_NOTE } from "@/lib/drafts";
import { imageVersion } from "@/lib/image";
import { formatCpu, formatMemory } from "@/lib/quantity";
import { draftSearch, observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, formatMonthDayYear } from "@/lib/time";
import { cn } from "@/lib/utils";

import {
	cellSummary,
	ENV_PREFIX,
	envRank,
	isDraftRow,
	linkEnvironment,
	replicasLabel,
	rowName,
	type DraftRow,
	type EnvCell,
	type ListRow,
	type ResourceRow,
} from "./table";

export interface ResourceCellValues {
	orgId: string;
	workspaceId: string;
	environments: Environment[];
	active: Environment | undefined;
	nowMs: number;
}

export const ResourceCellContext = createContext<ResourceCellValues | null>(null);

function useCells(): ResourceCellValues {
	const ctx = use(ResourceCellContext);
	if (ctx === null) throw new Error("ResourceCellContext provider missing");
	return ctx;
}

const DRAFT_NAME = "underline decoration-fg4 decoration-dashed hover:decoration-foreground";

const SUMMARY_TONE: Partial<Record<Glyph, string>> = { spin: "text-info", warn: "text-warn", err: "text-err" };

function EnvCellView({ cell }: { cell: EnvCell | undefined }) {
	const { nowMs } = useCells();
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
				<span className={cn("truncate font-mono text-[11.5px]", SUMMARY_TONE[style.glyph] ?? "text-fg3")}>
					{cellSummary(cell)}
				</span>
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

function NameCell({ row }: { row: ListRow }) {
	const { orgId, workspaceId } = useCells();
	const { search } = useLocation();
	const draft = isDraftRow(row);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<RowLink
				to={draft ? draftSearch(search, row.draft.id) : resourcePath(orgId, workspaceId, row.resource.id)}
				className={cn("truncate text-[14.5px] font-medium text-foreground", draft && DRAFT_NAME)}
			>
				{rowName(row)}
			</RowLink>
			<span className="truncate text-[12.5px] text-fg3">{draft ? DRAFT_NOTE : (row.resource.description ?? "")}</span>
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

function RowActions({ row }: { row: ResourceRow }) {
	const { orgId, workspaceId, environments, active } = useCells();
	const navigate = useNavigate();
	const { resource, address } = row;
	const env = linkEnvironment(row, environments, active);
	return (
		<ResourceRowMenu resource={resource} workspaceId={workspaceId} title={resource.name}>
			{row.cells.size === 0 && (
				<DropdownMenuItem onClick={() => void navigate(resourcePath(orgId, workspaceId, resource.id))}>
					<RocketIcon className="text-fg3" />
					Deploy
				</DropdownMenuItem>
			)}
			{row.cells.size > 0 && (
				<DropdownMenuItem
					onClick={() =>
						void navigate(observabilityPath(orgId, workspaceId, "logs", { env: env?.name ?? null, r: [resource.name] }))
					}
				>
					<ScrollTextIcon className="text-fg3" />
					Logs
				</DropdownMenuItem>
			)}
			<DropdownMenuItem
				onClick={() =>
					void navigate(resourcePath(orgId, workspaceId, resource.id, { tab: "settings", env: env?.name ?? null }))
				}
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

function envColumn(environment: Environment): DataColumn<ListRow> {
	return {
		id: `${ENV_PREFIX}${environment.name}`,
		header: environment.name.charAt(0).toUpperCase() + environment.name.slice(1),
		size: 172,
		accessorFn: (row) => envRank(row, environment.id),
		sortFn: "basic",
		cell: ({ row }) =>
			isDraftRow(row.original) && row.original.environmentId === environment.id ? (
				<DraftCellView row={row.original} />
			) : (
				<EnvCellView cell={row.original.cells.get(environment.id)} />
			),
	};
}

export function resourceColumns(environments: Environment[]): DataColumn<ListRow>[] {
	return [
		{
			id: "select",
			header: ({ table }) => (
				<Subscribe source={table.atoms.rowSelection}>
					{() => {
						const all = table.getIsAllPageRowsSelected();
						return (
							<Checkbox
								aria-label={all ? "Deselect this page" : "Select this page"}
								disabled={!table.getRowModel().rows.some((row) => row.getCanSelect())}
								checked={all}
								indeterminate={table.getIsSomePageRowsSelected() && !all}
								onCheckedChange={() => {
									table.toggleAllPageRowsSelected();
								}}
							/>
						);
					}}
				</Subscribe>
			),
			cell: ({ row }) =>
				row.getCanSelect() && (
					<Checkbox
						aria-label={`Select ${rowName(row.original)}`}
						checked={row.getIsSelected()}
						onCheckedChange={() => {
							row.toggleSelected();
						}}
					/>
				),
			size: 36,
			enableSorting: false,
			enableHiding: false,
			enableResizing: false,
			meta: { headClassName: "w-9" },
		},
		{
			id: "name",
			header: "Resource",
			size: 180,
			accessorFn: rowName,
			sortFn: (a, b) => rowName(a.original).localeCompare(rowName(b.original)),
			enableHiding: false,
			enableResizing: false,
			cell: ({ row }) => <NameCell row={row.original} />,
		},
		...environments.map(envColumn),
		{
			id: "size",
			header: "Size",
			size: 160,
			accessorFn: (row) => row.cpuMilli,
			sortDescFirst: true,
			sortFn: (a, b) => a.original.cpuMilli - b.original.cpuMilli || a.original.memMi - b.original.memMi,
			cell: ({ row: { original: row } }) => (
				<Mono text={row.cpuMilli === 0 ? null : `${formatCpu(row.cpuMilli)} · ${formatMemory(row.memMi)}`} />
			),
		},
		{
			id: "replicas",
			header: "Replicas",
			size: 92,
			enableSorting: false,
			meta: { defaultHidden: true },
			cell: ({ row }) => <Mono text={replicasLabel(row.original)} />,
		},
		{ id: "cost", header: "Est. cost", size: 110, enableSorting: false, meta: { soon: true } },
		{
			id: "address",
			header: "Address",
			size: 170,
			enableSorting: false,
			meta: { defaultHidden: true },
			cell: ({ row }) => <Mono text={row.original.address} />,
		},
		{
			id: "image",
			header: "Image",
			size: 200,
			enableSorting: false,
			meta: { defaultHidden: true },
			cell: ({ row }) => <Mono text={row.original.image} />,
		},
		{
			id: "created",
			header: "Created",
			size: 150,
			accessorFn: (row) => row.createdMs,
			sortFn: "basic",
			sortDescFirst: true,
			meta: { defaultHidden: true },
			cell: ({ row: { original: row } }) => <CreatedTime ms={row.createdMs} />,
		},
		{
			id: "actions",
			header: ({ table }) => <DataTableColumnsMenu table={table} />,
			cell: ({ row }) => (isDraftRow(row.original) ? null : <RowActions row={row.original} />),
			size: 40,
			enableSorting: false,
			enableHiding: false,
			enableResizing: false,
			meta: { headClassName: "w-10 text-right", cellClassName: "text-right" },
		},
	];
}

function CreatedTime({ ms }: { ms: number }) {
	const pref = useTimePreferences();
	return <Mono text={ms === 0 ? null : formatMonthDayYear(ms, pref)} />;
}
