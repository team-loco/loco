import { PanelLeftIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Kbd } from "@/components/design/Kbd";
import { useSidebar } from "@/components/design/Sidebar";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { cn } from "@/lib/utils";

export function SidebarToggle({ hideWhenOpen, className }: { hideWhenOpen?: boolean; className?: string }) {
	const { open, isMobile, openMobile, toggleSidebar } = useSidebar();
	const shown = isMobile ? openMobile : open;
	if (hideWhenOpen === true && shown) return null;
	const label = shown ? "Close sidebar" : "Open sidebar";
	return (
		<Tooltip>
			<TooltipTrigger
				render={
					<Button
						variant="ghost"
						size="icon"
						aria-label={label}
						onClick={toggleSidebar}
						className={cn("size-8 shrink-0 rounded-md hover:bg-shell-hover hover:text-foreground", className)}
					/>
				}
			>
				<PanelLeftIcon className="size-4" />
			</TooltipTrigger>
			<TooltipContent className="flex items-center gap-2">
				{label}
				<Kbd>⌘B</Kbd>
			</TooltipContent>
		</Tooltip>
	);
}
