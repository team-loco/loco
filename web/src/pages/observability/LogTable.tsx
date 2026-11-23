import { useTimePreferences } from "@/lib/use-time-preferences";
import { PlusIcon, XIcon } from "lucide-react";
import { functionalUpdate } from "@tanstack/react-table";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { Checkbox } from "@/components/design/Checkbox";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
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

function ColumnPicker({ columns, onColumns }: { columns: ColumnKey[]; onColumns: (next: ColumnKey[]) => void }) {
	const extra = columns.filter((c) => !PICKABLE_COLUMNS.includes(c));
	return (
		<Popover>
			<PopoverTrigger render={<Button variant="ghost" size="icon-xs" aria-label="Add a column" title="Add a column" />}>
				<PlusIcon />
			</PopoverTrigger>
			<PopoverContent align="end" aria-label="Columns" className="w-[200px] gap-0 p-1.5">
				<span className="px-2 pt-1.5 pb-1 text-[11.5px] text-fg3">Columns</span>
				{[...PICKABLE_COLUMNS, ...extra].map((c) => {
					const on = columns.includes(c);
					return (
						<label
							key={c}
							className="flex min-h-8 items-center gap-2.5 rounded-md px-2 py-1.5 text-[13px] hover:bg-panel"
						>
							<Checkbox
								checked={on}
								onCheckedChange={() => {
									onColumns(on ? columns.filter((x) => x !== c) : [...columns, c]);
								}}
							/>
							<span className={cn(!PICKABLE_COLUMNS.includes(c) && "font-mono text-[12.5px]")}>{columnLabel(c)}</span>
						</label>
					);
				})}
			</PopoverContent>
		</Popover>
	);
}

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
					<button
						type="button"
						aria-label={`Remove column ${columnLabel(c)}`}
						onClick={() => {
							onColumns(columns.filter((x) => x !== c));
						}}
						className="absolute top-1/2 right-3 flex size-[18px] -translate-y-1/2 items-center justify-center rounded-sm bg-panel text-fg3 opacity-0 group-hover/head:opacity-100 hover:bg-panel2 hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
					>
						<XIcon className="size-3" />
					</button>
				</>
			),
			size: columnWidth(c),
			minSize: MIN_COLUMN_WIDTH,
			maxSize: MAX_COLUMN_WIDTH,
			meta: { label: columnLabel(c), fit: true, fitMax: columnWidth(c) },
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
			header: () => <ColumnPicker columns={columns} onColumns={onColumns} />,
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
			empty={empty}
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
