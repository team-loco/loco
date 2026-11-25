import { useTimePreferences } from "@/lib/use-time-preferences";
import { functionalUpdate } from "@tanstack/react-table";
import type { ReactNode } from "react";

import { ColumnPicker, RemoveColumnButton } from "@/components/design/ColumnPicker";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Skeleton } from "@/components/design/Skeleton";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";

import {
	columnLabel,
	columnValue,
	columnWidth,
	MAX_COLUMN_WIDTH,
	MIN_COLUMN_WIDTH,
	PICKABLE_COLUMNS,
	type ColumnKey,
	type ColumnWidths,
} from "./columns";
import { bodyParts, fmtTs, jsonPreview, LevelBadge, levelStyle } from "./format";
import { LogRowMenu } from "./LogRowMenu";
import { WORKLOAD_TITLE, type LogRow } from "./rows";

const TIME_KEY = "time";

const LOG_SKELETON = (
	<div className="grid w-full grid-cols-[112px_92px_130px_minmax(0,1fr)] items-center gap-3">
		<Skeleton className="h-3 w-[84px]" />
		<Skeleton className="h-3 w-11" />
		<Skeleton className="h-3 w-16" />
		<Skeleton className="h-3 w-[70%]" />
	</div>
);

function logColumns({
	columns,
	dated,
	words,
	onColumns,
}: {
	columns: ColumnKey[];
	dated: boolean;
	words: string[];
	onColumns: (next: ColumnKey[]) => void;
}): DataColumn<LogRow>[] {
	return [
		{
			id: TIME_KEY,
			header: "Time",
			size: 120,
			minSize: MIN_COLUMN_WIDTH,
			maxSize: MAX_COLUMN_WIDTH,
			meta: { fit: true },
			cell: ({ row }) => <TimeCell row={row.original} dated={dated} />,
		},
		...columns.map((c): DataColumn<LogRow> => ({
			id: c,
			header: () => (
				<>
					<span className="truncate">{columnLabel(c)}</span>
					<RemoveColumnButton
						label={columnLabel(c)}
						onRemove={() => {
							onColumns(columns.filter((x) => x !== c));
						}}
					/>
				</>
			),
			size: columnWidth(c),
			minSize: MIN_COLUMN_WIDTH,
			maxSize: MAX_COLUMN_WIDTH,
			meta: { label: columnLabel(c), fit: true, fitMax: columnWidth(c), movable: true },
			cell: ({ row }) => <Cell row={row.original} column={c} />,
		})),
		{
			id: "message",
			header: "Message",
			size: 200,
			minSize: MIN_COLUMN_WIDTH,
			meta: { grow: true },
			cell: ({ row }) => <MessageCell row={row.original} words={words} />,
		},
		{
			id: "actions",
			header: () => (
				<ColumnPicker
					columns={columns}
					onColumns={onColumns}
					options={[...PICKABLE_COLUMNS, ...columns.filter((c) => !PICKABLE_COLUMNS.includes(c))].map((key) => ({
						key,
						label: columnLabel(key),
						mono: !PICKABLE_COLUMNS.includes(key),
					}))}
				/>
			),
			meta: { label: "Actions", pinEnd: true },
			cell: ({ row }) => <LogRowMenu row={row.original} />,
		},
	];
}

export function LogTable({
	rows,
	columns,
	dated,
	selectedKey,
	words,
	highlightAfter,
	loading,
	empty,
	widths,
	onWidths,
	onSelect,
	onColumns,
}: {
	rows: LogRow[];
	columns: ColumnKey[];
	/** Whether the time column shows the date, for ranges longer than a day. */
	dated: boolean;
	selectedKey: string | null;
	words: string[];
	highlightAfter: number | null;
	loading: boolean;
	empty: ReactNode;
	widths: ColumnWidths;
	onWidths: (next: ColumnWidths) => void;
	onSelect: (key: string) => void;
	onColumns: (next: ColumnKey[]) => void;
}) {
	const table = useDataTable("logs", {
		columns: logColumns({ columns, dated, words, onColumns }),
		data: rows,
		getRowId: (row) => row.key,
		state: { columnSizing: widths },
		onColumnSizingChange: (updater) => {
			onWidths(functionalUpdate(updater, widths));
		},
	});
	return (
		<DataTable
			table={table}
			label="Log lines"
			band
			className="text-[12px] [--row-h:34px] [&_td]:py-1"
			loading={loading}
			skeleton={LOG_SKELETON}
			skeletonRows={10}
			separators
			empty={empty}
			onColumnMove={(from, to) => {
				const next = columns.filter((c) => c !== from);
				next.splice(columns.indexOf(to), 0, from);
				onColumns(next);
			}}
			activeRowId={selectedKey}
			onRowClick={(row) => {
				onSelect(row.id);
			}}
			rowClassName={(row) =>
				row.id !== selectedKey &&
				highlightAfter !== null &&
				row.original.ts > highlightAfter &&
				"[--row-tint:var(--color-ok-bg)]"
			}
		/>
	);
}

function TimeCell({ row, dated }: { row: LogRow; dated: boolean }) {
	const timePref = useTimePreferences();
	return (
		<span className="font-mono whitespace-nowrap text-fg3">
			{dated ? fmtTs(row.ts, true, timePref) : formatClock(row.ts, true, timePref)}
		</span>
	);
}

function MessageCell({ row, words }: { row: LogRow; words: string[] }) {
	return (
		<span className={cn("min-w-0 truncate font-mono", levelStyle(row.level).body)}>
			{row.json !== null ? jsonPreview(row.json, words) : bodyParts(row.message, words)}
		</span>
	);
}

function Cell({ row, column }: { row: LogRow; column: ColumnKey }) {
	switch (column) {
		case "level":
			return <LevelBadge level={row.level} />;
		case "resource":
			return row.workload ? (
				<span className="block truncate font-mono text-[12px] text-fg3" title={WORKLOAD_TITLE}>
					{row.resourceName}
				</span>
			) : (
				<CellText text={row.resourceName} className="text-[12.5px]" />
			);
		default: {
			const value = columnValue(row, column);
			return value === "" ? (
				<span className="text-fg4">—</span>
			) : (
				<CellText text={column === "duration" ? `${value} ms` : value} className="font-mono text-fg2" />
			);
		}
	}
}
