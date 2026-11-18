import { ChevronDownIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "./Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "./DropdownMenu";
import { cn } from "@/lib/utils";

export interface FilterOption {
	value: string;
	label: string;
	count?: number;
}

export function FilterMenu({
	label,
	icon,
	value,
	options,
	onChange,
	allValue = "all",
	mono = false,
}: {
	label: string;
	icon?: ReactNode;
	value: string;
	options: FilterOption[];
	onChange: (value: string) => void;
	allValue?: string;
	mono?: boolean;
}) {
	const current = options.find((o) => o.value === value)?.label ?? "All";
	const active = value !== allValue;
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				aria-label={`${label}: ${current}`}
				render={
					<Button variant="outline" size="sm" className={cn("shrink-0 pr-2.5 pl-3", active && "border-foreground")} />
				}
			>
				{icon !== undefined && <span className="flex text-fg3">{icon}</span>}
				{current}
				<ChevronDownIcon className="size-3 text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent className="w-auto min-w-[220px] p-1">
				<DropdownMenuRadioGroup
					value={value}
					onValueChange={(v: string) => {
						onChange(v);
					}}
				>
					{options.map((o) => (
						<DropdownMenuRadioItem
							key={o.value}
							value={o.value}
							className={cn(
								"min-h-8 justify-between gap-3 py-0 pr-2.5 text-[12.5px] data-checked:bg-panel dark:data-checked:bg-panel2 [&>[data-slot=dropdown-menu-radio-item-indicator]]:hidden",
								mono && "font-mono",
							)}
						>
							<span>{o.label}</span>
							{o.count !== undefined && <span className="font-mono text-fg3">{o.count}</span>}
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
