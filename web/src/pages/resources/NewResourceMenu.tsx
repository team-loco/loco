import { ChevronDownIcon, PlusIcon, ServerIcon } from "lucide-react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";

export function NewResourceMenu({ serviceHref, disabled }: { serviceHref: string; disabled: boolean }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger disabled={disabled} render={<Button />}>
				<PlusIcon />
				New resource
				<ChevronDownIcon className="opacity-70" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[230px]">
				<DropdownMenuItem render={<Link to={serviceHref} />}>
					<span className="flex text-fg3">
						<ServerIcon />
					</span>
					Service
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
