import { Suspense, type ReactNode } from "react";

import { SidebarInset, SidebarProvider } from "@/components/design/Sidebar";
import { TooltipProvider } from "@/components/design/Tooltip";
import { AppLoading } from "@/context/AppLoader";

import { AppSidebar } from "./AppSidebar";
import { SuspensionBanner } from "./SuspensionBanner";
import { TopBar } from "./TopBar";

function readOpen(): boolean {
	const match = /(?:^|;\s*)sidebar_state=(true|false)/.exec(document.cookie);
	return match?.[1] !== "false";
}

export function AppShell({ children }: { children: ReactNode }) {
	return (
		<TooltipProvider>
			<SidebarProvider defaultOpen={readOpen()}>
				<AppSidebar />
				<SidebarInset>
					<TopBar />
					<SuspensionBanner />
					<div className="flex min-w-0 flex-1 flex-col">
						<Suspense fallback={<AppLoading />}>{children}</Suspense>
					</div>
				</SidebarInset>
			</SidebarProvider>
		</TooltipProvider>
	);
}
