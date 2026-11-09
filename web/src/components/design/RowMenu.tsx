import { EllipsisIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "./Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "./DropdownMenu";

export function RowMenu({ label, title, children }: { label: string; title?: ReactNode; children: ReactNode }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={label} className="text-fg2" />}>
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
