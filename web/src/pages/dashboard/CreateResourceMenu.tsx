import {
	ChevronDownIcon,
	DatabaseIcon,
	HardDriveIcon,
	LayersIcon,
	ListOrderedIcon,
	ServerIcon,
	ZapIcon,
	type LucideIcon,
} from "lucide-react";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { SoonTag } from "@/components/design/SoonTag";

const UNAVAILABLE: { label: string; icon: LucideIcon }[] = [
	{ label: "Database", icon: DatabaseIcon },
	{ label: "Cache", icon: LayersIcon },
	{ label: "Queue", icon: ListOrderedIcon },
	{ label: "Blob storage", icon: HardDriveIcon },
	{ label: "Function", icon: ZapIcon },
];

export function CreateResourceMenu({ onService, disabled }: { onService: () => void; disabled?: boolean | undefined }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button size="lg" className="rounded-sm pr-3 pl-3.5" disabled={disabled === true} />}
			>
				Create resource
				<ChevronDownIcon className="opacity-85" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-auto min-w-[220px]">
				<DropdownMenuItem className="h-[34px] gap-2.5" onClick={onService}>
					<ServerIcon className="size-[15px] text-fg3" />
					<span className="flex-1">Service</span>
				</DropdownMenuItem>
				{UNAVAILABLE.map(({ label, icon: Icon }) => (
					<DropdownMenuItem
						key={label}
						disabled
						title={`${label} resources are not available yet`}
						className="h-[34px] cursor-not-allowed gap-2.5 text-fg4 focus:bg-transparent focus:text-fg4 data-disabled:pointer-events-auto data-disabled:opacity-100"
					>
						<Icon className="size-[15px] text-fg4" />
						<span className="flex-1">{label}</span>
						<SoonTag />
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
