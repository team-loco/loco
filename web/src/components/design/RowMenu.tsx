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
	className,
	children,
}: {
	label: string;
	title?: ReactNode;
	className?: string | undefined;
	children: ReactNode;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label={label} className={cn("text-fg2", className)} />}
			>
				<EllipsisIcon className="size-4" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-auto min-w-[220px]">
				{title !== undefined && (
					<DropdownMenuGroup>
						<DropdownMenuLabel className="max-w-[260px] truncate">{title}</DropdownMenuLabel>
					</DropdownMenuGroup>
				)}
				{children}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
