import { Suspense, useEffect, type ReactNode } from "react";

import { PageLoading } from "@/components/design/Page";
import { SidebarInset, SidebarProvider } from "@/components/design/Sidebar";
import { TooltipProvider } from "@/components/design/Tooltip";
import { pageImporters } from "@/lib/lazy-pages";
import { useStoredValue } from "@/lib/storage";

import { AppSidebar } from "./AppSidebar";
import { SuspensionBanner } from "./SuspensionBanner";
import { TopBar } from "./TopBar";

export function AppShell({ children }: { children: ReactNode }) {
	const [sidebar, setSidebar] = useStoredValue("loco-sidebar", "open");
	useEffect(() => {
		for (const load of Object.values(pageImporters)) load().catch(() => undefined);
	}, []);
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
						<Suspense fallback={<PageLoading />}>{children}</Suspense>
					</div>
				</SidebarInset>
			</SidebarProvider>
		</TooltipProvider>
	);
}
