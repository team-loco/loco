import { SearchIcon } from "lucide-react";

import { Kbd } from "@/components/design/Kbd";

import { ShellBreadcrumb } from "./ShellBreadcrumb";
import { SidebarToggle } from "./SidebarToggle";

export function TopBar() {
	return (
		<header
			data-slot="top-bar"
			className="relative z-20 flex h-14 shrink-0 items-center gap-3 border-b border-line pr-[clamp(16px,3vw,28px)] pl-3 md:rounded-t-[12px]"
		>
			<SidebarToggle hideWhenOpen className="hover:bg-panel" />
			<ShellBreadcrumb />
			<div
				aria-disabled
				title="Search (soon)"
				className="hidden h-8 cursor-not-allowed items-center gap-2 rounded-md border border-line bg-raise pr-2 pl-2.5 text-[13px] text-fg4 md:flex"
			>
				<SearchIcon className="size-3.5" />
				<span>Search</span>
				<span className="text-xs">soon</span>
				<Kbd className="ml-2">⌘K</Kbd>
			</div>
		</header>
	);
}
