import { Suspense, useEffect, type ReactNode } from "react";

import { Page } from "@/components/design/Page";
import { SidebarInset, SidebarProvider } from "@/components/design/Sidebar";
import { Skeleton } from "@/components/design/Skeleton";
import { TooltipProvider } from "@/components/design/Tooltip";
import { pageImporters } from "@/lib/lazy-pages";

import { AppSidebar } from "./AppSidebar";
import { SuspensionBanner } from "./SuspensionBanner";
import { TopBar } from "./TopBar";

function readOpen(): boolean {
	const match = /(?:^|;\s*)sidebar_state=(true|false)/.exec(document.cookie);
	return match?.[1] !== "false";
}

function PageLoading() {
	return (
		<Page>
			<div data-slot="page-loader" role="status" aria-busy aria-label="Loading" className="contents">
				<Skeleton className="h-8 w-56" />
				<Skeleton className="h-64 w-full rounded-card" />
			</div>
		</Page>
	);
}

export function AppShell({ children }: { children: ReactNode }) {
	useEffect(() => {
		for (const load of Object.values(pageImporters)) void load();
	}, []);

	return (
		<TooltipProvider>
			<SidebarProvider defaultOpen={readOpen()}>
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
