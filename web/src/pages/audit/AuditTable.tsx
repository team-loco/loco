import { useTimePreferences } from "@/lib/use-time-preferences";
import { CpuIcon, KeyRoundIcon, ServerIcon } from "lucide-react";

import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { agoLabel, formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { dayLabel, type AuditRow } from "./model";

function ActorBadge({ row }: { row: AuditRow }) {
	const { actorType, actorEmail } = row.event;
	if (actorType === "user") {
		return <MemberAvatar name={row.actor.name} email={actorEmail} avatarUrl="" className="size-6" />;
	}
	const Icon = actorType === "api_token" ? KeyRoundIcon : actorType === "agent" ? ServerIcon : CpuIcon;
	return (
		<span className="flex size-6 shrink-0 items-center justify-center rounded-[6px] border border-line bg-background text-fg2">
			<Icon className="size-3" />
		</span>
	);
}

function auditColumns(nowMs: number): DataColumn<AuditRow>[] {
	return [
		{
			id: "time",
			header: "Time",
			size: 120,
			minSize: 90,
			maxSize: 640,
			cell: ({ row }) => <AuditTime row={row.original} nowMs={nowMs} />,
		},
		{
			id: "actor",
			header: "Actor",
			size: 220,
			minSize: 90,
			maxSize: 640,
			cell: ({ row: { original: row } }) => (
				<span className="flex min-w-0 items-center gap-[9px]">
					<ActorBadge row={row} />
					<span className="flex min-w-0 flex-col leading-[1.3]">
						<CellText text={row.actor.name} />
						{row.actor.detail !== "" && <CellText text={row.actor.detail} className="text-[11.5px] text-fg3" />}
					</span>
				</span>
			),
		},
		{
			id: "action",
			header: "Action",
			size: 300,
			minSize: 90,
			maxSize: 640,
			meta: { grow: true },
			cell: ({ row: { original: row } }) => (
				<span className="flex min-w-0 items-baseline gap-2">
					<span className="shrink-0">{row.label}</span>
					<CellText text={row.detail} className="font-mono text-[12px] text-fg2" />
				</span>
			),
		},
		{
			id: "where",
			header: "Where",
			size: 240,
			minSize: 90,
			maxSize: 640,
			meta: { growsLeft: true },
			cell: ({ row: { original: row } }) => (
				<span className="flex min-w-0 items-center gap-2 font-mono text-[12px] text-fg2">
					<CellText text={row.where} />
					{row.environment !== "" && (
						<span data-slot="chip" className="shrink-0 rounded-[5px] border border-line px-1.5 text-[11px] text-fg3">
							{row.environment}
						</span>
					)}
				</span>
			),
		},
	];
}

function AuditTime({ row, nowMs }: { row: AuditRow; nowMs: number }) {
	const timePref = useTimePreferences();
	return (
		<span className="font-mono text-[12px] text-fg2" title={agoLabel(row.ms, nowMs)}>
			{formatClock(row.ms, false, timePref)}
		</span>
	);
}

export function AuditTable({
	rows,
	highlightAfter,
	loading,
	selectedId,
	nowMs,
	onSelect,
}: {
	rows: readonly AuditRow[];
	highlightAfter?: bigint | undefined;
	loading: boolean;
	selectedId: string | null;
	nowMs: number;
	onSelect: (id: string) => void;
}) {
	const timePref = useTimePreferences();
	const table = useDataTable("audit", {
		columns: auditColumns(nowMs),
		data: rows,
		getRowId: (row) => row.event.id,
	});
	return (
		<DataTable
			table={table}
			label="Audit events"
			band
			className="[--row-h:48px] [&_td]:py-1.5"
			loading={loading}
			activeRowId={selectedId}
			onRowClick={(row) => {
				onSelect(row.id);
			}}
			groupBy={(row) => dayLabel(row.ms, nowMs, timePref)}
			rowClassName={(row) =>
				cn(
					"data-[state=selected]:[--row-tint:var(--background)]!",
					highlightAfter !== undefined &&
						row.original.event.seq > highlightAfter &&
						"motion-safe:animate-[audit-event-in_2s_ease-out]",
				)
			}
		/>
	);
}
