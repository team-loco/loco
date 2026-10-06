import { SearchIcon } from "lucide-react";

import { Kbd } from "@/components/design/Kbd";
import { SidebarTrigger } from "@/components/design/Sidebar";
import { cn } from "@/lib/utils";

import { useSidebarPeek } from "./AppShell";
import { ShellBreadcrumb } from "./ShellBreadcrumb";

export function TopBar() {
	const { pinned } = useSidebarPeek();

	return (
		<div className="flex h-14 shrink-0 items-center gap-3 px-5 text-md">
			<SidebarTrigger
				aria-pressed={pinned}
				className={cn("size-7 rounded-sm hover:bg-bg3", pinned && "bg-info-bg text-primary hover:bg-info-bg hover:text-primary")}
			/>
			<span className="mx-1 h-4 w-px bg-line" />
			<ShellBreadcrumb />
			<div className="flex-1" />
			<div
				aria-disabled
				title="Search (soon)"
				className="hidden h-8 w-[280px] cursor-not-allowed items-center gap-2 rounded-sm border border-line px-2.5 text-base text-fg4 md:flex"
			>
				<SearchIcon className="size-3.5" />
				<span className="flex-1">Search</span>
				<span className="text-xs">soon</span>
				<Kbd>⌘K</Kbd>
			</div>
		</div>
	);
}
