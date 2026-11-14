import { ChevronDownIcon, XIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { Button } from "./Button";
import { Command, CommandEmpty, CommandInput, CommandList } from "./Command";
import { Popover, PopoverContent, PopoverTrigger } from "./Popover";

/** A filter button that opens a searchable list; once set it shows the value and a clear button. */
export function FilterPopover({
	icon,
	label,
	value,
	mono = false,
	onClear,
	placeholder,
	footer,
	children,
}: {
	icon: ReactNode;
	label: string;
	value: string;
	mono?: boolean;
	onClear?: (() => void) | undefined;
	placeholder?: string | undefined;
	footer?: ReactNode;
	children: (close: () => void) => ReactNode;
}) {
	const [open, setOpen] = useState(false);
	const set = value !== "";
	const clearable = set && onClear !== undefined;
	return (
		<span className="flex shrink-0 items-center">
			<Popover open={open} onOpenChange={setOpen}>
				<PopoverTrigger
					render={
						<Button
							variant="outline"
							className={clearable ? "max-w-[260px] rounded-r-none border-r-0 px-2.5" : "max-w-[260px] px-2.5"}
						/>
					}
				>
					<span className="flex text-fg3">{icon}</span>
					{set ? <span className={mono ? "truncate font-mono" : "truncate"}>{value}</span> : label}
					{!clearable && <ChevronDownIcon className="size-3 text-fg3" />}
				</PopoverTrigger>
				<PopoverContent align="start" className="w-[300px] p-0" aria-label={`Filter by ${label.toLowerCase()}`}>
					<Command>
						{placeholder !== undefined && <CommandInput placeholder={placeholder} />}
						<CommandList>
							<CommandEmpty>Nothing matches.</CommandEmpty>
							{children(() => {
								setOpen(false);
							})}
						</CommandList>
					</Command>
					{footer}
				</PopoverContent>
			</Popover>
			{clearable && (
				<Button
					variant="outline"
					size="icon"
					aria-label={`Clear ${label.toLowerCase()} filter`}
					className="rounded-l-none"
					onClick={onClear}
				>
					<XIcon />
				</Button>
			)}
		</span>
	);
}
