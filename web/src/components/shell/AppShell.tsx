import type { ReactNode } from "react";

import { SidebarInset, SidebarProvider } from "@/components/design/Sidebar";
import { TooltipProvider } from "@/components/design/Tooltip";
import { ShellProvider } from "@/context/ShellContext";

import { AppSidebar } from "./AppSidebar";
import { TopBar } from "./TopBar";

export function AppShell({ children }: { children: ReactNode }) {
	return (
		<ShellProvider>
			<TooltipProvider>
				<SidebarProvider>
					<AppSidebar />
					<SidebarInset className="min-w-0 bg-background">
						<TopBar />
						<main className="flex min-w-0 flex-1 flex-col">{children}</main>
					</SidebarInset>
				</SidebarProvider>
			</TooltipProvider>
		</ShellProvider>
	);
}
