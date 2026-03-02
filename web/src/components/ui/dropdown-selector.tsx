import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ChevronDown } from "lucide-react";
import { ReactNode, ComponentType } from "react";

interface DropdownSelectorProps {
	label: ReactNode;
	icon?: ComponentType<{ className?: string }>;
	items: {
		value: string | number;
		label: ReactNode;
	}[];
	onSelect: (value: string | number) => void;
	contentWidth?: string;
}

export function DropdownSelector({
	label,
	icon: Icon,
	items,
	onSelect,
	contentWidth = "w-40",
}: DropdownSelectorProps) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button variant="outline" size="sm" className="gap-1.5">
					{Icon && <Icon className="h-3.5 w-3.5" />}
					{label}
					<ChevronDown className="h-3.5 w-3.5 opacity-60" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className={contentWidth}>
				{items.map((item) => (
					<DropdownMenuItem
						key={item.value}
						onClick={() => { onSelect(item.value); }}
						className="cursor-pointer"
					>
						{item.label}
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
