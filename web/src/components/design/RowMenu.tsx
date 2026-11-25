import { EllipsisIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

import { Button } from "./Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "./DropdownMenu";

export function RowMenu({
	label,
	title,
	monoTitle = false,
	children,
}: {
	label: string;
	title?: ReactNode;
	/** Sets the title in the monospace face, for a name such as a resource's or a token's. */
	monoTitle?: boolean;
	children: ReactNode;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label={label} data-row-menu className="text-fg2" />}
			>
				<EllipsisIcon className="size-4" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-auto min-w-[220px]">
				{title !== undefined && (
					<DropdownMenuGroup>
						<DropdownMenuLabel className={cn("max-w-[260px] truncate", monoTitle && "font-mono text-[11.5px]")}>
							{title}
						</DropdownMenuLabel>
					</DropdownMenuGroup>
				)}
				{children}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
