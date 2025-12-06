"use client";

import { ChevronRight, Plus } from "lucide-react";

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

export function NavWorkspaces({
	workspaces,
	onWorkspaceClick,
	onAppClick,
	onCreateApp,
	activeAppId,
}: {
	workspaces: {
		id: bigint;
		name: string;
		isActive?: boolean;
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
	return (
		<SidebarGroup className="group-data-[collapsible=icon]:hidden">
			<SidebarGroupLabel>Workspaces</SidebarGroupLabel>
			<SidebarMenu>
				{workspaces.map((workspace) => (
					<Collapsible
						key={workspace.id.toString()}
						asChild
						defaultOpen={workspace.isActive}
					>
						<SidebarMenuItem>
							<SidebarMenuButton
								onClick={() =>
									onWorkspaceClick?.(workspace.id)
								}
								tooltip={workspace.name}
								isActive={workspace.isActive}
							>
								<span>{workspace.name}</span>
							</SidebarMenuButton>
							{workspace.apps?.length ? (
								<>
									<CollapsibleTrigger asChild>
										<SidebarMenuAction className="data-[state=open]:rotate-90">
											<ChevronRight />
											<span className="sr-only">
												Toggle
											</span>
										</SidebarMenuAction>
									</CollapsibleTrigger>
									<CollapsibleContent>
										<SidebarMenuSub>
											{workspace.apps?.map((app) => (
												<SidebarMenuSubItem
													key={app.id.toString()}
												>
													<SidebarMenuSubButton
														onClick={() =>
															onAppClick?.(
																app.id,
																workspace.id
															)
														}
														isActive={
															activeAppId ===
															app.id
														}
													>
														<span>{app.name}</span>
													</SidebarMenuSubButton>
												</SidebarMenuSubItem>
											))}
										</SidebarMenuSub>
									</CollapsibleContent>
								</>
							) : null}
							{!workspace.apps?.length && (
								<SidebarMenuAction
									onClick={() =>
										onCreateApp?.(workspace.id)
									}
									className="hover:bg-sidebar-accent"
								>
									<Plus className="h-4 w-4" />
									<span className="sr-only">Create app</span>
								</SidebarMenuAction>
							)}
						</SidebarMenuItem>
					</Collapsible>
				))}
			</SidebarMenu>
		</SidebarGroup>
	);
}
