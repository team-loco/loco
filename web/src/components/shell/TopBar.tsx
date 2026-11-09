import { AlertsDrawer } from "./AlertsDrawer";
import { CommandMenu } from "./CommandMenu";
import { ShellBreadcrumb } from "./ShellBreadcrumb";
import { SidebarToggle } from "./SidebarToggle";

export function TopBar() {
	return (
		<header
			data-slot="top-bar"
			className="relative z-20 flex h-14 shrink-0 items-center gap-3 border-b border-line pr-[clamp(16px,3vw,28px)] pl-3 md:rounded-t-overlay"
		>
			<SidebarToggle hideWhenOpen className="hover:bg-panel" />
			<ShellBreadcrumb />
			<AlertsDrawer />
			<CommandMenu />
		</header>
	);
}
