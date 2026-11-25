import { PlusIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Checkbox } from "@/components/design/Checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { cn } from "@/lib/utils";

export interface ColumnOption {
	key: string;
	label: string;
	mono?: boolean;
}

/** The button in an added column's header that removes the column. */
export function RemoveColumnButton({ label, onRemove }: { label: string; onRemove: () => void }) {
	return (
		<button
			type="button"
			aria-label={`Remove column ${label}`}
			onClick={onRemove}
			className="absolute top-1/2 right-3 flex size-[18px] -translate-y-1/2 items-center justify-center rounded-sm bg-panel text-fg3 opacity-0 group-hover/head:opacity-100 hover:bg-panel2 hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
		>
			<XIcon className="size-3" />
		</button>
	);
}

/** A plus button that opens a checklist of the columns a table can add. */
export function ColumnPicker({
	columns,
	options,
	onColumns,
	label = "Add a column",
}: {
	columns: string[];
	options: ColumnOption[];
	onColumns: (next: string[]) => void;
	label?: string;
}) {
	return (
		<Popover>
			<PopoverTrigger render={<Button variant="ghost" size="icon-xs" aria-label={label} title={label} />}>
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
