import { useTimePreferences } from "@/lib/use-time-preferences";
import { CopyIcon, PencilIcon, RocketIcon, ScrollTextIcon, Settings2Icon, Trash2Icon } from "lucide-react";
import { createContext, use, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import type { AllocationCoverage } from "@gen/loco/capacity/v1/capacity_pb";
import type { ResourceReservation } from "@gen/loco/resource/v1/resource_pb";
import { Skeleton } from "@/components/design/Skeleton";
import { reservationLabel } from "@/lib/reservation";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { useCopy } from "@/hooks/useCopy";
import { useDrafts } from "@/hooks/useDrafts";
import { ResourceRowMenu } from "@/components/ResourceRowMenu";
import { CellText, DataTableColumnsMenu, type DataColumn } from "@/components/design/DataTable";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { SoonTag } from "@/components/design/SoonTag";
import { ROLLOUT_STYLE, resourceStatusStyle } from "@/components/design/StatusBadge";
import { type Glyph, StatusGlyph } from "@/components/design/StatusGlyph";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { DRAFT_NOTE } from "@/lib/drafts";
import { formatCpu, formatMemory } from "@/lib/quantity";
import { draftSearch, observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, formatMonthDayYear } from "@/lib/time";
import { cn } from "@/lib/utils";
import { DiscardDraftDialog } from "@/pages/resources/DiscardDraftDialog";

import {
	cellSummary,
	ENV_PREFIX,
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
	creators: ReadonlyMap<string, string>;
	reservations: ReadonlyMap<string, ResourceReservation>;
	reservationCoverage: AllocationCoverage | undefined;
	reservationsLoading: boolean;
}

export const ResourceCellContext = createContext<ResourceCellValues | null>(null);

function useCells(): ResourceCellValues {
	const ctx = use(ResourceCellContext);
	if (ctx === null) throw new Error("ResourceCellContext provider missing");
	return ctx;
}

const SUMMARY_TONE: Partial<Record<Glyph, string>> = {
	spin: "text-info",
	warn: "text-warn",
	err: "text-err",
};

function EnvCellView({ cell }: { cell: EnvCell | undefined }) {
	const { nowMs } = useCells();
	if (cell === undefined) return <span className="text-fg4">—</span>;
	const style = resourceStatusStyle(cell.status);
	const rollout = cell.rollout === undefined ? undefined : ROLLOUT_STYLE[cell.rollout];
	const tip = [
		style.label,
		rollout?.label ?? "",
		cell.note,
		cell.deployedMs === 0 ? "" : `deployed ${agoLabel(cell.deployedMs, nowMs)}`,
	]
		.filter((s) => s !== "")
		.join(", ");
	return (
		<Tooltip>
			<TooltipTrigger
				render={<button type="button" aria-label={`${tip}, ${cellSummary(cell)}`} />}
				className="flex max-w-full min-w-0 flex-col items-start gap-0.5 rounded-sm text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
			>
				<span className="flex max-w-full min-w-0 items-center gap-2">
					<StatusGlyph glyph={style.glyph} />
					<span className="truncate font-mono text-[12.5px] text-foreground">{cell.tag === "" ? "—" : cell.tag}</span>
				</span>
				<span className={cn("truncate font-mono text-[11.5px]", SUMMARY_TONE[style.glyph] ?? "text-fg3")}>
					{cellSummary(cell)}
					{rollout !== undefined && (
						<span className={SUMMARY_TONE[rollout.glyph]}>
							{", "}
							{rollout.short.toLowerCase()}
						</span>
					)}
				</span>
			</TooltipTrigger>
			<TooltipContent>{tip}</TooltipContent>
		</Tooltip>
	);
}

function DraftCellView({ row }: { row: DraftRow }) {
	const desired = (row.replicas?.min ?? 0) * row.regions.length;
	return (
		<span className="flex max-w-full min-w-0 flex-col items-start gap-0.5">
			<span className="flex max-w-full min-w-0 items-center gap-2">
				<StatusGlyph glyph="draft" />
				<span className="truncate font-mono text-[12.5px] text-foreground">draft</span>
			</span>
			<span className="max-w-full truncate font-mono text-[11.5px] text-fg3">
				{cellSummary({ ready: 0, desired, regions: row.regions })}
			</span>
		</span>
	);
}

export function rowPath(row: ListRow, orgId: string, workspaceId: string, search: string): string {
	return isDraftRow(row) ? draftSearch(search, row.draft.id) : resourcePath(orgId, workspaceId, row.resource.id);
}

function NameCell({ row }: { row: ListRow }) {
	const { orgId, workspaceId } = useCells();
	const { search } = useLocation();
	const draft = isDraftRow(row);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<CellText
				text={rowName(row)}
				render={<Link to={rowPath(row, orgId, workspaceId, search)} />}
				className="text-[14.5px] font-medium text-foreground"
			/>
			<CellText text={draft ? DRAFT_NOTE : (row.resource.description ?? "")} className="text-[12.5px] text-fg3" />
		</div>
	);
}

function Mono({ text }: { text: string | null }) {
	if (text === null || text === "") return <span className="text-fg4">—</span>;
	return <CellText text={text} className="font-mono text-[12px] text-fg2" />;
}

function RowActions({ row }: { row: ResourceRow }) {
	const { orgId, workspaceId, environments, active } = useCells();
	const navigate = useNavigate();
	const [, copy] = useCopy();
	const { resource, address } = row;
	const env = linkEnvironment(row, environments, active);
	return (
		<ResourceRowMenu resource={resource} workspaceId={workspaceId} title={resource.name} monoTitle>
			{row.cells.size === 0 && (
				<DropdownMenuItem onClick={() => void navigate(resourcePath(orgId, workspaceId, resource.id))}>
					<RocketIcon className="text-fg3" />
					Deploy
				</DropdownMenuItem>
			)}
			{row.cells.size > 0 && (
				<DropdownMenuItem
					onClick={() =>
						void navigate(
							observabilityPath(orgId, workspaceId, "logs", {
								env: env?.slug ?? null,
								r: [resource.slug],
							}),
						)
					}
				>
					<ScrollTextIcon className="text-fg3" />
					Logs
				</DropdownMenuItem>
			)}
			<DropdownMenuItem
				onClick={() =>
					void navigate(
						resourcePath(orgId, workspaceId, resource.id, {
							tab: "settings",
							env: env?.slug ?? null,
						}),
					)
				}
			>
				<Settings2Icon className="text-fg3" />
				Settings
			</DropdownMenuItem>
			{address !== null && (
				<DropdownMenuItem
					onClick={() => {
						copy("address", `https://${address}`, `Copied https://${address}`);
					}}
				>
					<CopyIcon className="text-fg3" />
					Copy address
				</DropdownMenuItem>
			)}
			<DropdownMenuSeparator />
		</ResourceRowMenu>
	);
}

function DraftActions({ row }: { row: DraftRow }) {
	const { orgId, workspaceId } = useCells();
	const navigate = useNavigate();
	const { search } = useLocation();
	const { discard } = useDrafts(orgId, workspaceId, row.environmentId);
	const [discarding, setDiscarding] = useState(false);
	const { draft } = row;
	return (
		<>
			<RowMenu label={`Actions for ${draft.name}`} title={draft.name} monoTitle>
				<DropdownMenuItem onClick={() => void navigate(draftSearch(search, draft.id))}>
					<PencilIcon className="text-fg3" />
					Edit draft
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<DropdownMenuItem
					variant="destructive"
					onClick={() => {
						setDiscarding(true);
					}}
				>
					<Trash2Icon />
					Discard draft
				</DropdownMenuItem>
			</RowMenu>
			<DiscardDraftDialog
				name={draft.name}
				open={discarding}
				onDiscard={() => {
					discard(draft.id);
				}}
				onClose={() => {
					setDiscarding(false);
				}}
			/>
		</>
	);
}

function envColumn(environment: Environment): DataColumn<ListRow> {
	return {
		id: `${ENV_PREFIX}${environment.slug}`,
		header: environment.name.charAt(0).toUpperCase() + environment.name.slice(1),
		size: 172,
		minSize: 150,
		maxSize: 280,
		meta: { fit: true },
		accessorFn: (row) => row.cells.get(environment.id)?.status,
		cell: ({ row }) =>
			isDraftRow(row.original) && row.original.environmentId === environment.id ? (
				<DraftCellView row={row.original} />
			) : (
				<EnvCellView cell={row.original.cells.get(environment.id)} />
			),
	};
}

function ReservationCell({ row, memory }: { row: ListRow; memory: boolean }) {
	const { reservations, reservationCoverage, reservationsLoading } = useCells();
	if (isDraftRow(row)) return <Mono text={null} />;
	if (reservationsLoading) return <Skeleton className="h-3 w-20" />;
	const reservation = reservations.get(row.resource.id);
	if (reservation === undefined) return <Mono text="Not reported" />;
	const value = memory ? reservation.memoryBytes : reservation.cpuMillicores;
	const builds = memory ? reservation.workspaceBuildMemoryBytes : reservation.workspaceBuildCpuMillicores;
	return (
		<div className="flex flex-col gap-0.5 font-mono text-[12px]">
			<span>{reservationLabel(value, memory, reservationCoverage)}</span>
			{builds > 0n && (
				<span className="text-[11px] text-fg3" title="Workspace build reservations across all environments">
					Builds {reservationLabel(builds, memory, reservationCoverage)}
				</span>
			)}
		</div>
	);
}

export function resourceColumns(environments: Environment[]): DataColumn<ListRow>[] {
	return [
		{
			id: "name",
			header: "Resource",
			size: 220,
			minSize: 160,
			maxSize: 360,
			accessorFn: rowName,
			enableHiding: false,
			meta: { fit: true },
			cell: ({ row }) => <NameCell row={row.original} />,
		},
		...environments.map(envColumn),
		{
			id: "size",
			header: "Configured size",
			size: 140,
			minSize: 120,
			maxSize: 220,
			meta: { fit: true },
			accessorFn: (row) => row.cpuMilli,
			sortDescFirst: true,
			cell: ({ row: { original: row } }) => (
				<Mono text={row.cpuMilli === 0 ? null : `${formatCpu(row.cpuMilli)}, ${formatMemory(row.memMi)}`} />
			),
		},
		...([false, true] as const).map((memory): DataColumn<ListRow> => ({
			id: memory ? "reserved-memory" : "reserved-cpu",
			header: () => (
				<span title="Actual runtime pod reservations across all regions in the selected environment">
					{memory ? "Reserved memory" : "Reserved CPU"}
				</span>
			),
			size: 170,
			minSize: 130,
			enableSorting: false,
			meta: { label: memory ? "Reserved memory" : "Reserved CPU" },
			cell: ({ row }) => <ReservationCell row={row.original} memory={memory} />,
		})),
		{
			id: "replicas",
			header: "Replicas",
			size: 92,
			minSize: 80,
			maxSize: 140,
			meta: { fit: true, defaultHidden: true },
			cell: ({ row }) => <Mono text={replicasLabel(row.original)} />,
		},
		{
			id: "cost",
			header: "Est. cost",
			size: 90,
			minSize: 72,
			maxSize: 110,
			enableSorting: false,
			meta: { fit: true },
			cell: () => <SoonTag />,
		},
		{
			id: "address",
			header: "Address",
			size: 170,
			minSize: 100,
			maxSize: 300,
			meta: { fit: true, defaultHidden: true },
			cell: ({ row }) => <Mono text={row.original.address} />,
		},
		{
			id: "image",
			header: "Image",
			size: 200,
			minSize: 120,
			maxSize: 360,
			meta: { fit: true, defaultHidden: true },
			cell: ({ row }) => <Mono text={row.original.image} />,
		},
		{
			id: "created",
			header: "Created",
			size: 150,
			minSize: 100,
			maxSize: 200,
			accessorFn: (row) => row.createdMs,
			sortDescFirst: true,
			meta: { fit: true, defaultHidden: true },
			cell: ({ row: { original: row } }) => (
				<Created ms={row.createdMs} by={isDraftRow(row) ? "" : row.resource.createdBy} />
			),
		},
		{
			id: "actions",
			header: ({ table }) => <DataTableColumnsMenu table={table} />,
			cell: ({ row }) =>
				isDraftRow(row.original) ? <DraftActions row={row.original} /> : <RowActions row={row.original} />,
			enableHiding: false,
			meta: { pinEnd: true },
		},
	];
}

function Created({ ms, by }: { ms: number; by: string }) {
	const pref = useTimePreferences();
	const { creators } = useCells();
	const date = ms === 0 ? null : formatMonthDayYear(ms, pref);
	const name = creators.get(by);
	if (name === undefined) return <Mono text={date} />;
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<CellText text={name} className="text-[13px] text-foreground" />
			<CellText text={date ?? "—"} className="font-mono text-[11.5px] text-fg3" />
		</div>
	);
}
