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
import { SoonTag } from "@/components/design/SoonTag";

const TYPES: { type: ResourceType; label: string; icon: ReactNode }[] = [
	{ type: ResourceType.SERVICE, label: "Service", icon: <ServerIcon /> },
	{ type: ResourceType.DATABASE, label: "Database", icon: <DatabaseIcon /> },
	{ type: ResourceType.CACHE, label: "Cache", icon: <ZapIcon /> },
	{ type: ResourceType.QUEUE, label: "Queue", icon: <ListOrderedIcon /> },
	{ type: ResourceType.BLOB, label: "Blob storage", icon: <ArchiveIcon /> },
	{ type: ResourceType.FUNCTION, label: "Function", icon: <SquareFunctionIcon /> },
];

export function NewResourceMenu({ serviceHref, disabled }: { serviceHref: string; disabled: boolean }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger disabled={disabled} render={<Button />}>
				<PlusIcon />
				New resource
				<ChevronDownIcon className="opacity-70" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[220px]">
				{TYPES.map((t) =>
					t.type === ResourceType.SERVICE ? (
						<DropdownMenuItem key={t.type} render={<Link to={serviceHref} />}>
							<span className="flex text-fg3">{t.icon}</span>
							{t.label}
						</DropdownMenuItem>
					) : (
						<DropdownMenuItem key={t.type} disabled className="text-fg3">
							<span className="flex text-fg4">{t.icon}</span>
							<span className="flex-1">{t.label}</span>
							<SoonTag />
						</DropdownMenuItem>
					),
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
