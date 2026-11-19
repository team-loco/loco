import { CopyIcon, EllipsisIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { useCopy } from "@/hooks/useCopy";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { logJson, logMessageText, type LogRow } from "./rows";

export function LogRowMenu({ row }: { row: LogRow }) {
	const timePref = useTimePreferences();
	const [, copy] = useCopy();
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={
					<Button
						variant="ghost"
						size="icon-xs"
						aria-label="More for this line"
						title="More"
						className="opacity-0 group-focus-within/row:opacity-100 group-hover/row:opacity-100 data-popup-open:opacity-100"
					/>
				}
			>
				<EllipsisIcon className="size-4" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[200px]">
				<DropdownMenuItem
					onClick={() => {
						copy("msg", logMessageText(row), "Copied message");
					}}
				>
					<CopyIcon />
					Copy message
				</DropdownMenuItem>
				<DropdownMenuItem
					onClick={() => {
						copy("json", logJson(row, timePref), "Copied as JSON");
					}}
				>
					<CopyIcon />
					Copy as JSON
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
