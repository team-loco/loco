import {
	ArchiveIcon,
	ChevronDownIcon,
	DatabaseIcon,
	ListOrderedIcon,
	PlusIcon,
	ServerIcon,
	SquareFunctionIcon,
	ZapIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { TYPE_FILTERS } from "@/pages/dashboard/resourceRows";

const ICONS: Record<number, ReactNode> = {
	[ResourceType.SERVICE]: <ServerIcon />,
	[ResourceType.DATABASE]: <DatabaseIcon />,
	[ResourceType.CACHE]: <ZapIcon />,
	[ResourceType.QUEUE]: <ListOrderedIcon />,
	[ResourceType.BLOB]: <ArchiveIcon />,
	[ResourceType.FUNCTION]: <SquareFunctionIcon />,
};

export function NewResourceMenu({ serviceHref, disabled }: { serviceHref: string; disabled: boolean }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger disabled={disabled} render={<Button />}>
				<PlusIcon />
				New resource
				<ChevronDownIcon className="opacity-70" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[230px]">
				{TYPE_FILTERS.map((t) =>
					t.type === ResourceType.SERVICE ? (
						<DropdownMenuItem key={t.value} render={<Link to={serviceHref} />}>
							<span className="flex text-fg3">{ICONS[t.type]}</span>
							New service
						</DropdownMenuItem>
					) : (
						<DropdownMenuItem key={t.value} disabled className="data-disabled:opacity-100">
							<span className="flex text-fg3">{ICONS[t.type]}</span>
							<span className="flex-1 text-fg3">{t.label}</span>
							<span className="text-[11.5px] text-fg3">Soon</span>
						</DropdownMenuItem>
					),
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
