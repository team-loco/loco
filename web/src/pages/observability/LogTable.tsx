import { PlusIcon, XIcon } from "lucide-react";

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

import { columnLabel, columnValue, columnWidth, PICKABLE_COLUMNS, type ColumnKey } from "./columns";
import { bodyParts, fmtTs, jsonPreview, levelStyle } from "./format";
import type { LogRow } from "./rows";

const TIME_WIDTH = 176;
const COMPACT_TIME_WIDTH = 124;
const MESSAGE_MIN = "minmax(260px,1fr)";
const ADD_WIDTH = 36;

export function LogTable({
	rows,
	columns,
	compact,
	selectedKey,
	words,
	highlightAfter,
	onSelect,
	onColumns,
}: {
	rows: LogRow[];
	columns: ColumnKey[];
	compact: boolean;
	selectedKey: string | null;
	words: string[];
	highlightAfter: number | null;
	onSelect: (key: string) => void;
	onColumns: (next: ColumnKey[]) => void;
}) {
	const grid = [
		`${String(compact ? COMPACT_TIME_WIDTH : TIME_WIDTH)}px`,
		...columns.map((c) => `${String(columnWidth(c))}px`),
		MESSAGE_MIN,
		`${String(ADD_WIDTH)}px`,
	].join(" ");
	const extra = columns.filter((c) => !PICKABLE_COLUMNS.includes(c));
	return (
		<div className="w-max min-w-full">
			<div>
				<div
					className="grid h-[38px] items-center border-b border-line2 text-[12px] text-fg3"
					style={{ gridTemplateColumns: grid }}
				>
					<span className="px-3">Time</span>
					{columns.map((c) => (
						<span key={c} className="group/col flex min-w-0 items-center gap-1 px-3">
							<span className="truncate">{columnLabel(c)}</span>
							<button
								type="button"
								aria-label={`Remove column ${columnLabel(c)}`}
								onClick={() => {
									onColumns(columns.filter((x) => x !== c));
								}}
								className="flex size-[18px] items-center justify-center rounded-sm text-fg3 opacity-0 group-hover/col:opacity-100 hover:bg-panel2 hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
							>
								<XIcon className="size-3" />
							</button>
						</span>
					))}
					<span className="px-3">Message</span>
					<span className="flex justify-end">
						<DropdownMenu>
							<DropdownMenuTrigger
								render={<Button variant="ghost" size="icon-xs" aria-label="Add a column" title="Add a column" />}
							>
								<PlusIcon />
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="w-[220px]">
								<DropdownMenuGroup>
									<DropdownMenuLabel>Columns</DropdownMenuLabel>
									{[...PICKABLE_COLUMNS, ...extra].map((c) => {
										const on = columns.includes(c);
										return (
											<DropdownMenuCheckboxItem
												key={c}
												checked={on}
												onCheckedChange={() => {
													onColumns(on ? columns.filter((x) => x !== c) : [...columns, c]);
												}}
											>
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
						<button
							key={l.key}
							type="button"
							aria-pressed={open}
							onClick={() => {
								onSelect(l.key);
							}}
							className={cn(
								"grid min-h-[34px] w-full items-center rounded-lg text-left text-[12px] transition-[background] duration-900 ease-out hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
								i % 2 === 1 && "bg-[color-mix(in_oklab,var(--panel2)_45%,transparent)]",
								open && "bg-background shadow-[0_0_0_1px_var(--line2)] hover:bg-background",
								!open && fresh && "bg-ok-bg",
							)}
							style={{ gridTemplateColumns: grid }}
						>
							<span className="truncate px-3 font-mono whitespace-nowrap text-fg3">
								{compact ? formatClock(l.ts, true) : fmtTs(l.ts, true)}
							</span>
							{columns.map((c) => (
								<span key={c} className="min-w-0 truncate px-3">
									<Cell row={l} column={c} />
								</span>
							))}
							<span className={cn("min-w-0 truncate px-3 font-mono", style.body)}>
								{l.json !== null ? jsonPreview(l.json, words) : bodyParts(l.entry.body, words)}
							</span>
							<span />
						</button>
					);
				})}
			</div>
		</div>
	);
}

function Cell({ row, column }: { row: LogRow; column: ColumnKey }) {
	switch (column) {
		case "level":
			return <span className={cn("text-[12.5px]", levelStyle(row.level).text)}>{row.label}</span>;
		case "resource":
			return <span className="block truncate text-[12.5px]">{row.resourceName}</span>;
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
