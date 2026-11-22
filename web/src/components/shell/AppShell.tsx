import { Suspense, type ReactNode } from "react";

import { SidebarInset, SidebarProvider } from "@/components/design/Sidebar";
import { TooltipProvider } from "@/components/design/Tooltip";
import { AppLoading } from "@/context/AppLoader";
import { useStoredValue } from "@/lib/storage";

import { AppSidebar } from "./AppSidebar";
import { SuspensionBanner } from "./SuspensionBanner";
import { TopBar } from "./TopBar";

export function AppShell({ children }: { children: ReactNode }) {
	const [sidebar, setSidebar] = useStoredValue("loco-sidebar", "open");
	return (
		<TooltipProvider>
			<SidebarProvider
				open={sidebar !== "closed"}
				onOpenChange={(open) => {
					setSidebar(open ? "open" : "closed");
				}}
			>
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
