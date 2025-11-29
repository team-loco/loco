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
import {
	ArrowRight,
	Bell,
	ChevronDown,
	ChevronsUpDown,
	Grid,
	Home,
	Plus,
	Settings,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
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
	const [activeWorkspaceName, setActiveWorkspaceName] = useState<string | null>(
		null
	);
	const [expandedWorkspaces, setExpandedWorkspaces] = useState<Set<bigint>>(
		new Set(activeWorkspaceId ? [activeWorkspaceId] : [])
	);
	const [selectedOrgId, setSelectedOrgId] = useState<bigint | null>(null);
	const [eventCount] = useState(0);

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

	// Get workspaces for first org
	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);

	const workspaces = workspacesRes?.workspaces ?? [];

	// Get apps for active workspace
	const appsQuery = useQuery(
		listApps,
		{ workspaceId: activeWorkspaceId ?? 0n },
		{ enabled: !!activeWorkspaceId }
	);

	// Update header based on current route
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
	}, [
		activeAppId,
		activeWorkspaceId,
		appsQuery.data?.apps,
		workspaces,
		setHeader,
	]);

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
		<Sidebar className="border-r-2 border-border">
			<SidebarHeader>
				<SidebarMenu>
					<SidebarMenuItem>
						<DropdownMenu>
							<DropdownMenuTrigger className="focus-visible:ring-0" asChild>
								<SidebarMenuButton
									size="lg"
									className="data-[state=open]:bg-main data-[state=open]:text-main-foreground data-[state=open]:outline-border data-[state=open]:outline-2"
								>
									<div className="flex items-center gap-2 flex-1">
										<span className="font-heading truncate">
											{activeOrg?.name}
										</span>
									</div>
									<ChevronsUpDown className="ml-auto" />
								</SidebarMenuButton>
							</DropdownMenuTrigger>
							<DropdownMenuContent
								className="w-[--radix-dropdown-menu-trigger-width] min-w-56 rounded-base"
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
										className="gap-2 p-1.5"
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
								asChild
								onClick={() => navigate("/dashboard")}
								className="h-10"
							>
								<button className="flex items-center gap-2">
									<Home className="h-4 w-4" />
									<span>Dashboard</span>
								</button>
							</SidebarMenuButton>
						</SidebarMenuItem>
					</SidebarMenu>
				</SidebarGroup>

				{/* Workspaces & Apps Tree */}
				<SidebarGroup>
					<SidebarGroupLabel>Workspaces</SidebarGroupLabel>
					<SidebarMenu className="space-y-1 pl-4">
						{workspaces.length === 0 ? (
							<>
								<Skeleton className="h-8 w-full rounded-neo" />
								<Skeleton className="h-8 w-full rounded-neo" />
							</>
						) : (
							workspaces.map((ws) => (
								<div key={ws.id.toString()}>
									<SidebarMenuItem>
										<div className="flex items-center w-full">
											<button
												onClick={() => toggleWorkspaceExpansion(ws.id)}
												className="p-1 -ml-2 hover:bg-secondary-background rounded"
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
													setActiveWorkspaceName(ws.name);
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
													<span className="text-xs font-heading">Apps</span>
													<button
														onClick={() => navigate("/create-app")}
														className="p-0.5 hover:bg-secondary-background rounded-neo"
														title="Create App"
													>
														<Plus className="h-3 w-3" />
													</button>
												</div>
												<SidebarMenu className="space-y-1 pl-4">
													{appsQuery.isLoading ? (
														<>
															<Skeleton className="h-7 w-full rounded-neo" />
															<Skeleton className="h-7 w-full rounded-neo" />
														</>
													) : appsQuery.data?.apps &&
													  appsQuery.data.apps.length > 0 ? (
														appsQuery.data.apps.map((app) => (
															<SidebarMenuItem key={app.id.toString()}>
																<SidebarMenuButton
																	asChild
																	onClick={() => handleAppClick(app.id)}
																	isActive={activeAppId === app.id}
																>
																	<button className="flex items-center gap-2 text-sm">
																		<Grid className="h-4 w-4 shrink-0" />
																		<span className="truncate">{app.name}</span>
																	</button>
																</SidebarMenuButton>
															</SidebarMenuItem>
														))
													) : (
														<p className="text-xs text-foreground opacity-50 px-3 py-1">
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

			{/* Footer with User Menu & Theme Toggle */}
			<SidebarFooter className="border-t-2 border-border">
				<ThemeToggle />

				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<button className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-neo transition-colors">
							<div className="flex items-center gap-2 min-w-0">
								<Avatar className="h-8 w-8">
									<AvatarImage src={user?.avatarUrl} alt={user?.name} />
									<AvatarFallback>
										{user?.name?.charAt(0).toUpperCase()}
									</AvatarFallback>
								</Avatar>
								<div className="hidden sm:block min-w-0 text-left">
									<p className="text-xs font-heading truncate">{user?.name}</p>
									<p className="text-xs opacity-75 truncate">{user?.email}</p>
								</div>
							</div>
							<ChevronsUpDown className="h-4 w-4 shrink-0" />
						</button>
					</DropdownMenuTrigger>
					<DropdownMenuContent
						align="end"
						className="w-80 max-h-96 overflow-y-auto"
					>
						<DropdownMenuItem
							onClick={() => navigate("/events")}
							className="cursor-pointer"
						>
							<Bell className="h-4 w-4 mr-2" />
							<div className="flex items-center gap-2">
								<span>Recent Events</span>
								{eventCount > 0 && (
									<span className="ml-auto text-xs bg-destructive text-white px-2 py-0.5 rounded-full">
										{eventCount > 9 ? "9+" : eventCount}
									</span>
								)}
								<ArrowRight className="h-4 w-4" />
							</div>
						</DropdownMenuItem>
						<DropdownMenuSeparator />
						<DropdownMenuItem
							onClick={() => navigate("/profile")}
							className="cursor-pointer"
						>
							<Settings className="h-4 w-4 mr-2" />
							<span>Profile Settings</span>
						</DropdownMenuItem>
						<DropdownMenuSeparator />
						<DropdownMenuItem
							onClick={handleLogout}
							className="cursor-pointer text-error-text"
						>
							<span>Logout</span>
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</SidebarFooter>
		</Sidebar>
	);
}
