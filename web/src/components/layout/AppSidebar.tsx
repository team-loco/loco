import { Zap } from "lucide-react";
import * as React from "react";

import {
	Sidebar,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarRail,
	useSidebar,
} from "@/components/ui/sidebar";

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
	const { toggleSidebar } = useSidebar();

	React.useEffect(() => {
		const handleKeyDown = (e: KeyboardEvent) => {
			const isMac = /Mac|iPhone|iPad|iPod/.test(
				typeof navigator !== "undefined"
					?  
						navigator.userAgent || navigator.platform || ""
					: "",
			);
			const isToggleKey = e.key === "b" || e.key === "B";
			const isCorrectModifier = isMac ? e.metaKey : e.ctrlKey;

			if (isToggleKey && isCorrectModifier && !e.shiftKey && !e.altKey) {
				e.preventDefault();
				toggleSidebar();
			}
		};

		window.addEventListener("keydown", handleKeyDown);
		return () => {
			window.removeEventListener("keydown", handleKeyDown);
		};
	}, [toggleSidebar]);

	return (
		<Sidebar {...props}>
			<SidebarHeader className="pt-14">
				<SidebarMenu>
					<SidebarMenuItem>
						<SidebarMenuButton
							size="lg"
							className="hover:bg-transparent active:bg-transparent focus-visible:bg-transparent data-[state=open]:bg-transparent cursor-default"
						>
							<div className="flex aspect-square size-8 items-center justify-center rounded-md bg-white border">
								<Zap className="size-4" fill="#000" />
							</div>
							<div className="flex flex-col gap-0.5 leading-none">
								<span className="font-bold text-sm">LOCO</span>
								<span className="text-[10px] font-medium opacity-70">
									Deploy & Scale
								</span>
							</div>
						</SidebarMenuButton>
					</SidebarMenuItem>
				</SidebarMenu>
			</SidebarHeader>

			<SidebarRail />
		</Sidebar>
	);
}
