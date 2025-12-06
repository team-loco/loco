import { ChevronDown, Grid, Home, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
} from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { useHeader } from "@/context/HeaderContext";
import { listApps } from "@/gen/app/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { getCurrentUser, logout } from "@/gen/user/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ChevronsUpDown } from "lucide-react";
import { toast } from "sonner";
import { ThemeToggle } from "./ThemeToggle";

export function AppSidebar() {
	const navigate = useNavigate();
	const location = useLocation();
	const { setHeader } = useHeader();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const activeWorkspaceId = workspaceFromUrl ? BigInt(workspaceFromUrl) : null;

	const appIdMatch = location.pathname.match(/\/app\/(\d+)/);
	const activeAppId = appIdMatch ? BigInt(appIdMatch[1]) : null;
	const { mutate: logoutMutation } = useMutation(logout);
	const [expandedWorkspaces, setExpandedWorkspaces] = useState<Set<bigint>>(
		new Set(activeWorkspaceId ? [activeWorkspaceId] : [])
	);
	const [selectedOrgId, setSelectedOrgId] = useState<bigint | null>(null);

	const toggleWorkspaceExpansion = (workspaceId: bigint) => {
		setExpandedWorkspaces((prev) => {
			const next = new Set(prev);
			if (next.has(workspaceId)) {
				next.delete(workspaceId);
			} else {
				next.add(workspaceId);
			}
			return next;
		});
	};

	const { data: userRes } = useQuery(getCurrentUser, {});
	const user = userRes?.user ?? null;

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const orgs = orgsRes?.orgs ?? [];
	const firstOrgId = orgs[0]?.id ?? null;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);

	const workspaces = workspacesRes?.workspaces ?? [];

	const appsQuery = useQuery(
		listApps,
		{ workspaceId: activeWorkspaceId ?? 0n },
		{ enabled: !!activeWorkspaceId }
	);

	useEffect(() => {
		const appName = appsQuery.data?.apps?.find(
			(app) => app.id === activeAppId
		)?.name;

		if (activeAppId && appName) {
			setHeader(
				<div className="flex flex-col">
					<h1 className="text-2xl font-heading">{appName}</h1>
				</div>
			);
		} else if (activeWorkspaceId) {
			const workspaceName = workspaces.find(
				(ws) => ws.id === activeWorkspaceId
			)?.name;
			if (workspaceName) {
				setHeader(
					<div className="flex flex-col">
						<h1 className="text-2xl font-heading">{workspaceName}</h1>
					</div>
				);
			}
		} else {
			setHeader(
				<div className="flex flex-col">
					<h1 className="text-2xl font-heading">Dashboard</h1>
				</div>
			);
		}
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [activeAppId, activeWorkspaceId, appsQuery.data, setHeader]);

	const handleLogout = () => {
		logoutMutation(
			{},
			{
				onSuccess: () => {
					toast.success("Logged out successfully");
					navigate("/login");
				},
				onError: () => {
					toast.error("Failed to logout");
				},
			}
		);
	};

	const handleWorkspaceClick = (workspaceId: bigint) => {
		// Expand the workspace to show apps
		setExpandedWorkspaces((prev) => new Set([...prev, workspaceId]));
		navigate(`/dashboard?workspace=${workspaceId}`);
	};

	const handleAppClick = (appId: bigint) => {
		navigate(
			`/app/${appId}${
				activeWorkspaceId ? `?workspace=${activeWorkspaceId}` : ""
			}`
		);
	};

	const activeOrg = orgs.find(
		(org) => org.id === (selectedOrgId || orgs[0]?.id)
	);

	return (
		<Sidebar>
			<SidebarHeader>
				<SidebarMenu>
					<SidebarMenuItem>
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<SidebarMenuButton
									size="lg"
									className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
								>
									<div className="flex items-center gap-2 flex-1">
										<span className="font-heading truncate">
											{activeOrg?.name}
										</span>
									</div>
									<ChevronsUpDown className="ml-auto h-4 w-4" />
								</SidebarMenuButton>
							</DropdownMenuTrigger>
							<DropdownMenuContent
								className="w-[--radix-dropdown-menu-trigger-width] min-w-56"
								align="start"
								side="right"
								sideOffset={4}
							>
								<DropdownMenuLabel className="text-sm font-heading">
									Organizations
								</DropdownMenuLabel>
								{orgs.map((org) => (
									<DropdownMenuItem
										key={org.id.toString()}
										onClick={() => setSelectedOrgId(org.id)}
										className="gap-2"
									>
										<span>{org.name}</span>
									</DropdownMenuItem>
								))}
							</DropdownMenuContent>
						</DropdownMenu>
					</SidebarMenuItem>
				</SidebarMenu>
			</SidebarHeader>

			<SidebarContent className="space-y-0">
				{/* Dashboard Quick Access */}
				<SidebarGroup>
					<SidebarMenu>
						<SidebarMenuItem>
							<SidebarMenuButton
								tooltip="Dashboard"
								onClick={() => navigate("/dashboard")}
								isActive={!activeAppId && !activeWorkspaceId}
							>
								<Home className="h-4 w-4" />
								<span>Dashboard</span>
							</SidebarMenuButton>
						</SidebarMenuItem>
					</SidebarMenu>
				</SidebarGroup>

				{/* Workspaces & Apps */}
				<SidebarGroup>
					<SidebarGroupLabel>Workspaces</SidebarGroupLabel>
					<SidebarMenu className="space-y-1 pl-4">
						{workspaces.length === 0 ? (
							<>
								<Skeleton className="h-8 w-full rounded-lg" />
								<Skeleton className="h-8 w-full rounded-lg" />
							</>
						) : (
							workspaces.map((ws) => (
								<div key={ws.id.toString()}>
									<SidebarMenuItem>
										<div className="flex items-center w-full">
											<button
												onClick={() => toggleWorkspaceExpansion(ws.id)}
												className="p-1 -ml-2 hover:bg-sidebar-accent rounded"
											>
												<ChevronDown
													className={`h-4 w-4 transition-transform ${
														expandedWorkspaces.has(ws.id) ? "" : "-rotate-90"
													}`}
												/>
											</button>
											<SidebarMenuButton
												onClick={() => {
													handleWorkspaceClick(ws.id);
												}}
												isActive={activeWorkspaceId === ws.id && !activeAppId}
												className="flex-1"
											>
												<span>{ws.name}</span>
											</SidebarMenuButton>
										</div>
									</SidebarMenuItem>

									{/* Apps under this workspace */}
									{activeWorkspaceId === ws.id &&
										expandedWorkspaces.has(ws.id) && (
											<div className="space-y-1 mt-1">
												<div className="flex items-center justify-between px-4 py-1">
													<span className="text-xs font-heading text-sidebar-foreground/70">
														Apps
													</span>
													<button
														onClick={() => navigate("/create-app")}
														className="p-0.5 hover:bg-sidebar-accent rounded-lg"
														title="Create App"
													>
														<Plus className="h-3 w-3" />
													</button>
												</div>
												<SidebarMenu className="space-y-1 pl-4">
													{appsQuery.isLoading ? (
														<>
															<Skeleton className="h-7 w-full rounded-lg" />
															<Skeleton className="h-7 w-full rounded-lg" />
														</>
													) : appsQuery.data?.apps &&
													  appsQuery.data.apps.length > 0 ? (
														appsQuery.data.apps.map((app) => (
															<SidebarMenuItem key={app.id.toString()}>
																<SidebarMenuButton
																	onClick={() => handleAppClick(app.id)}
																	isActive={activeAppId === app.id}
																>
																	<Grid className="h-4 w-4 shrink-0" />
																	<span className="truncate">{app.name}</span>
																</SidebarMenuButton>
															</SidebarMenuItem>
														))
													) : (
														<p className="text-xs text-sidebar-foreground/50 px-3 py-1">
															No apps yet
														</p>
													)}
												</SidebarMenu>
											</div>
										)}
								</div>
							))
						)}
					</SidebarMenu>
				</SidebarGroup>
			</SidebarContent>

			<SidebarFooter className="border-t">
				<ThemeToggle />

				<SidebarMenu>
					<SidebarMenuItem>
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<SidebarMenuButton
									size="lg"
									className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
								>
									<Avatar className="h-8 w-8 rounded-lg">
										<AvatarImage src={user?.avatarUrl} alt={user?.name} />
										<AvatarFallback className="rounded-lg">
											{user?.name?.charAt(0).toUpperCase()}
										</AvatarFallback>
									</Avatar>
									<div className="grid flex-1 text-left text-sm leading-tight">
										<span className="truncate font-semibold">{user?.name}</span>
										<span className="truncate text-xs">{user?.email}</span>
									</div>
									<ChevronsUpDown className="ml-auto h-4 w-4" />
								</SidebarMenuButton>
							</DropdownMenuTrigger>
							<DropdownMenuContent
								className="w-[--radix-dropdown-menu-trigger-width] min-w-56"
								side="right"
								align="end"
								sideOffset={4}
							>
								<DropdownMenuLabel className="p-0 font-normal">
									<div className="flex items-center gap-2 px-1 py-1.5 text-left text-sm">
										<Avatar className="h-8 w-8 rounded-lg">
											<AvatarImage src={user?.avatarUrl} alt={user?.name} />
											<AvatarFallback className="rounded-lg">
												{user?.name?.charAt(0).toUpperCase()}
											</AvatarFallback>
										</Avatar>
										<div className="grid flex-1 text-left text-sm leading-tight">
											<span className="truncate font-semibold">
												{user?.name}
											</span>
											<span className="truncate text-xs">{user?.email}</span>
										</div>
									</div>
								</DropdownMenuLabel>
								<DropdownMenuSeparator />
								<DropdownMenuItem
									onClick={() => navigate("/profile")}
									className="cursor-pointer"
								>
									Settings
								</DropdownMenuItem>
								<DropdownMenuSeparator />
								<DropdownMenuItem
									onClick={handleLogout}
									className="cursor-pointer"
								>
									Logout
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					</SidebarMenuItem>
				</SidebarMenu>
			</SidebarFooter>
		</Sidebar>
	);
}
