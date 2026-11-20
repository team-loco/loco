import { useTimePreferences } from "@/lib/use-time-preferences";
import { CheckIcon, PlusIcon, XIcon } from "lucide-react";

import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Button } from "@/components/design/Button";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";

import { columnLabel, columnValue, columnWidth, PICKABLE_COLUMNS, type ColumnKey, type ColumnWidths } from "./columns";
import { bodyParts, fmtTs, jsonPreview, LEVEL_LABEL, levelStyle } from "./format";
import { LIST_HEAD_CLASS, listRowClass, useResizableColumns } from "./ListGrid";
import { LogRowMenu } from "./LogRowMenu";
import { WORKLOAD_TITLE, type LogRow } from "./rows";

const TIME_KEY = "time";
const MONO_CHAR_WIDTH = 7.5;
const CELL_PADDING = 28;
// "12:00:00.000 PM" is 15 characters, "23:59:59.999" 12; the date adds "Sep 30, ".
const timeChars = (dated: boolean, clock: "12" | "24") => (clock === "12" ? 15 : 12) + (dated ? 8 : 0);
const MESSAGE_MIN = "minmax(260px,1fr)";
const ADD_WIDTH = 36;

export function LogTable({
	rows,
	columns,
	dated,
	selectedKey,
	words,
	highlightAfter,
	onSelect,
	onColumns,
	widths,
	onWidths,
}: {
	rows: LogRow[];
	columns: ColumnKey[];
	widths: ColumnWidths;
	onWidths: (next: ColumnWidths) => void;
	/** Whether the time column shows the date, for ranges longer than a day. */
	dated: boolean;
	selectedKey: string | null;
	words: string[];
	highlightAfter: number | null;
	onSelect: (key: string) => void;
	onColumns: (next: ColumnKey[]) => void;
}) {
	const timePref = useTimePreferences();
	const defaultTimeWidth = Math.ceil(timeChars(dated, timePref.clock) * MONO_CHAR_WIDTH) + CELL_PADDING;
	const widthOf = (key: ColumnKey, w: ColumnWidths) =>
		key === TIME_KEY ? (w[key] ?? defaultTimeWidth) : columnWidth(key, w);
	const { live, handle } = useResizableColumns(widths, onWidths, widthOf);
	const grid = [
		`${String(widthOf(TIME_KEY, live))}px`,
		...columns.map((c) => `${String(columnWidth(c, live))}px`),
		MESSAGE_MIN,
		`${String(ADD_WIDTH)}px`,
	].join(" ");
	const extra = columns.filter((c) => !PICKABLE_COLUMNS.includes(c));
	return (
		<div className="w-max min-w-full">
			<div>
				<div className={LIST_HEAD_CLASS} style={{ gridTemplateColumns: grid }}>
					<span className="relative flex h-full min-w-0 items-center px-3">
						<span className="truncate">Time</span>
						{handle(TIME_KEY, "Time")}
					</span>
					{columns.map((c) => (
						<span key={c} className="group/col relative flex h-full min-w-0 items-center px-3">
							<span className="truncate" title={columnLabel(c)}>
								{columnLabel(c)}
							</span>
							<button
								type="button"
								aria-label={`Remove column ${columnLabel(c)}`}
								onClick={() => {
									onColumns(columns.filter((x) => x !== c));
								}}
								className="absolute top-1/2 right-3 flex size-[18px] -translate-y-1/2 items-center justify-center rounded-sm bg-panel text-fg3 opacity-0 group-hover/col:opacity-100 hover:bg-panel2 hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
							>
								<XIcon className="size-3" />
							</button>
							{handle(c, columnLabel(c))}
						</span>
					))}
					<span className="flex h-full items-center px-3">Message</span>
					<span className="flex h-full items-center justify-end">
						<DropdownMenu>
							<DropdownMenuTrigger
								render={<Button variant="ghost" size="icon-xs" aria-label="Add a column" title="Add a column" />}
							>
								<PlusIcon />
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="w-[200px]">
								<DropdownMenuGroup>
									<DropdownMenuLabel>Columns</DropdownMenuLabel>
									{[...PICKABLE_COLUMNS, ...extra].map((c) => {
										const on = columns.includes(c);
										return (
											<DropdownMenuCheckboxItem
												key={c}
												className="pr-2.5 [&>[data-slot=dropdown-menu-checkbox-item-indicator]]:hidden"
												checked={on}
												onCheckedChange={() => {
													onColumns(on ? columns.filter((x) => x !== c) : [...columns, c]);
												}}
											>
												<span
													aria-hidden
													className={cn(
														"flex size-3.5 shrink-0 items-center justify-center rounded-[4px]",
														on ? "bg-foreground text-background" : "shadow-[inset_0_0_0_1.5px_var(--line2)]",
													)}
												>
													{on && <CheckIcon className="size-2.5" strokeWidth={3} />}
												</span>
												<span className={cn(!PICKABLE_COLUMNS.includes(c) && "font-mono text-[12.5px]")}>
													{columnLabel(c)}
												</span>
											</DropdownMenuCheckboxItem>
										);
									})}
								</DropdownMenuGroup>
							</DropdownMenuContent>
						</DropdownMenu>
					</span>
				</div>
			</div>
			<div className="flex flex-col gap-0.5 pt-1">
				{rows.map((l, i) => {
					const open = l.key === selectedKey;
					const style = levelStyle(l.level);
					const fresh = highlightAfter !== null && l.ts > highlightAfter;
					return (
						<div key={l.key} className="group/row relative">
							<button
								type="button"
								aria-pressed={open}
								onClick={() => {
									onSelect(l.key);
								}}
								className={cn(listRowClass(i, open), !open && fresh && "bg-ok-bg")}
								style={{ gridTemplateColumns: grid }}
							>
								<span className="px-3 font-mono whitespace-nowrap text-fg3">
									{dated ? fmtTs(l.ts, true, timePref) : formatClock(l.ts, true, timePref)}
								</span>
								{columns.map((c) => (
									<span key={c} className="min-w-0 truncate px-3">
										<Cell row={l} column={c} />
									</span>
								))}
								<span className={cn("min-w-0 truncate px-3 font-mono", style.body)}>
									{l.json !== null ? jsonPreview(l.json, words) : bodyParts(l.message, words)}
								</span>
								<span />
							</button>
							<div className="absolute inset-y-0 right-0 flex w-9 items-center justify-center">
								<LogRowMenu row={l} />
							</div>
						</div>
					);
				})}
			</div>
		</div>
	);
}

function Cell({ row, column }: { row: LogRow; column: ColumnKey }) {
	switch (column) {
		case "level":
			return <span className={cn("text-[12.5px]", levelStyle(row.level).text)}>{LEVEL_LABEL[row.level]}</span>;
		case "resource":
			return row.workload ? (
				<span className="block truncate font-mono text-[12px] text-fg3" title={WORKLOAD_TITLE}>
					{row.resourceName}
				</span>
			) : (
				<span className="block truncate text-[12.5px]">{row.resourceName}</span>
			);
		default: {
			const value = columnValue(row, column);
			return value === "" ? (
				<span className="text-fg4">—</span>
			) : (
				<span className="font-mono text-fg2" title={value}>
					{value}
				</span>
			);
		}
	}
}
