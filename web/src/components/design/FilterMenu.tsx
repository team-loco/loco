import { ChevronDownIcon } from "lucide-react";

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
	value,
	options,
	onChange,
	allValue = "all",
}: {
	label: string;
	value: string;
	options: FilterOption[];
	onChange: (value: string) => void;
	allValue?: string;
}) {
	const current = options.find((o) => o.value === value);
	const active = value !== allValue;
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={
					<Button variant="outline" className={cn("shrink-0 px-2.5", active && "border-foreground")} />
				}
			>
				<span>
					{label}: <span className="font-medium">{current?.label ?? "All"}</span>
				</span>
				<ChevronDownIcon className="size-3 text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent className="w-auto min-w-[200px]">
				<DropdownMenuRadioGroup value={value} onValueChange={(v: string) => { onChange(v); }}>
					{options.map((o) => (
						<DropdownMenuRadioItem key={o.value} value={o.value} className="justify-between gap-3 pr-2.5">
							<span>{o.label}</span>
							{o.count !== undefined && <span className="text-sm text-fg3">{o.count}</span>}
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
