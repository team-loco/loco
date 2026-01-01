import { ChevronRight } from "lucide-react";

import {
	Collapsible,
	CollapsibleContent,
	CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
	SidebarGroup,
	SidebarGroupLabel,
	SidebarMenu,
	SidebarMenuAction,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarMenuSub,
	SidebarMenuSubButton,
	SidebarMenuSubItem,
} from "@/components/ui/sidebar";

import { useState } from "react";

export function NavWorkspaces({
	workspaces,
	onWorkspaceClick,
	onAppClick,
	activeAppId,
}: {
	workspaces: {
		id: bigint;
		name: string;
		isActive?: boolean;
		hasApps?: boolean;
		apps?: {
			id: bigint;
			name: string;
		}[];
	}[];
	onWorkspaceClick?: (workspaceId: bigint) => void;
	onAppClick?: (appId: bigint, workspaceId: bigint) => void;
	onCreateApp?: (workspaceId: bigint) => void;
	activeAppId?: bigint;
}) {
	const activeWorkspaceWithApp = workspaces.find(
		(ws) => ws.apps?.some((app) => app.id === activeAppId)
	);

	const [openWorkspaces, setOpenWorkspaces] = useState<Set<string>>(() => {
		const initial = new Set<string>();
		if (activeWorkspaceWithApp) {
			initial.add(activeWorkspaceWithApp.id.toString());
		}
		return initial;
	});

	return (
		<SidebarGroup className="group-data-[collapsible=icon]:hidden">
			<SidebarGroupLabel>Workspaces</SidebarGroupLabel>
			<SidebarMenu>
				{workspaces.map((workspace) => {
					const workspaceId = workspace.id.toString();
					const isOpen = openWorkspaces.has(workspaceId) || workspace.isActive;

					return (
						<Collapsible
							key={workspaceId}
							asChild
							open={isOpen}
							onOpenChange={(open) => {
								const newSet = new Set(openWorkspaces);
								if (open) {
									newSet.add(workspaceId);
								} else {
									newSet.delete(workspaceId);
								}
								setOpenWorkspaces(newSet);
							}}
							className="group/workspace"
						>
							<SidebarMenuItem>
								<CollapsibleTrigger asChild>
									<SidebarMenuButton
										onClick={() => onWorkspaceClick?.(workspace.id)}
										tooltip={workspace.name}
										isActive={workspace.isActive}
										className={`${
											workspace.isActive
												? "bg-sidebar-accent text-sidebar-accent-foreground"
												: ""
										}`}
									>
										<span>{workspace.name}</span>
									</SidebarMenuButton>
								</CollapsibleTrigger>
								<CollapsibleTrigger asChild>
									<SidebarMenuAction
										onClick={(e) => e.stopPropagation()}
										className="group-data-[state=open]/workspace:rotate-90 transition-transform"
									>
										<ChevronRight className="h-4 w-4" />
									</SidebarMenuAction>
								</CollapsibleTrigger>
								<CollapsibleContent>
									<SidebarMenuSub>
										{workspace.apps?.map((app) => (
											<SidebarMenuSubItem key={app.id.toString()}>
												<SidebarMenuSubButton
													onClick={() => onAppClick?.(app.id, workspace.id)}
													isActive={activeAppId === app.id}
												>
													<span>{app.name}</span>
												</SidebarMenuSubButton>
											</SidebarMenuSubItem>
										))}
									</SidebarMenuSub>
								</CollapsibleContent>
							</SidebarMenuItem>
						</Collapsible>
					);
				})}
			</SidebarMenu>
		</SidebarGroup>
	);
}
