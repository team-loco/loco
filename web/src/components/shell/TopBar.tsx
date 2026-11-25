import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useInSystem } from "@/hooks/useInSystem";
import { AlertsDrawer } from "./AlertsDrawer";
import { CommandMenu } from "./CommandMenu";
import { ShellBreadcrumb } from "./ShellBreadcrumb";
import { SidebarToggle } from "./SidebarToggle";

export function TopBar() {
	const { activeWorkspaceId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const system = useInSystem();
	return (
		<header
			data-slot="top-bar"
			className="relative z-20 flex h-14 shrink-0 items-center gap-3 border-b max-[760px]:gap-2 border-line pr-[clamp(16px,3vw,28px)] pl-3 md:rounded-t-overlay"
		>
			<SidebarToggle hideWhenOpen className="hover:bg-panel" />
			<ShellBreadcrumb />
			<CommandMenu />
			{/* Each environment gets its own activity drawer, which starts watching for deploys when it mounts. */}
			<div className="flex justify-end min-[761px]:flex-1 min-[761px]:basis-0">
				{!system && <AlertsDrawer key={`${activeWorkspaceId ?? ""}/${active?.id ?? ""}`} />}
			</div>
		</header>
	);
}
