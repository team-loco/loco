import { PlusIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Checkbox } from "@/components/design/Checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { cn } from "@/lib/utils";

export interface ColumnOption {
	key: string;
	label: string;
	mono?: boolean;
}

/** A plus button that opens a checklist of the columns a table can add. */
export function ColumnPicker({
	columns,
	options,
	onColumns,
}: {
	columns: string[];
	options: ColumnOption[];
	onColumns: (next: string[]) => void;
}) {
	return (
		<Popover>
			<PopoverTrigger render={<Button variant="ghost" size="icon-xs" aria-label="Add a column" title="Add a column" />}>
				<PlusIcon />
			</PopoverTrigger>
			<PopoverContent align="end" aria-label="Columns" className="w-[200px] gap-0 p-1.5">
				<span className="px-2 pt-1.5 pb-1 text-[11.5px] text-fg3">Columns</span>
				{options.map((o) => {
					const on = columns.includes(o.key);
					return (
						<label
							key={o.key}
							className="flex min-h-8 items-center gap-2.5 rounded-md px-2 py-1.5 text-[13px] hover:bg-panel"
						>
							<Checkbox
								checked={on}
								onCheckedChange={() => {
									onColumns(on ? columns.filter((x) => x !== o.key) : [...columns, o.key]);
								}}
							/>
							<span className={cn(o.mono === true && "font-mono text-[12.5px]")}>{o.label}</span>
						</label>
					);
				})}
			</PopoverContent>
		</Popover>
	);
}
