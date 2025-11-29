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
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
} from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { listApps } from "@/gen/app/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { getCurrentUser, logout } from "@/gen/user/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
	ArrowRight,
	Bell,
	ChevronsUpDown,
	Grid,
	Home,
	Plus,
	Settings,
} from "lucide-react";
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { toast } from "sonner";
import { OrgsSidebar } from "./sidebar/OrgsSidebar";
import { ThemeToggle } from "./ThemeToggle";

export function AppSidebar() {
	const navigate = useNavigate();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const activeWorkspaceId = workspaceFromUrl ? BigInt(workspaceFromUrl) : null;
	const { mutate: logoutMutation } = useMutation(logout);
	const [expandedOrgs, setExpandedOrgs] = useState<Set<bigint>>(new Set());
	const [activeWorkspaceName, setActiveWorkspaceName] = useState<string | null>(
		null
	);
	const [eventCount] = useState(0);

	const { data: userRes } = useQuery(getCurrentUser, {});
	const user = userRes?.user ?? null;

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});

	const orgs = orgsRes?.orgs ?? [];

	// Get apps for active workspace
	const appsQuery = useQuery(
		listApps,
		{ workspaceId: activeWorkspaceId ?? 0n },
		{ enabled: !!activeWorkspaceId }
	);

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

	const toggleOrgExpansion = (orgId: bigint) => {
		setExpandedOrgs((prev) => {
			const next = new Set(prev);
			if (next.has(orgId)) {
				next.delete(orgId);
			} else {
				next.add(orgId);
			}
			return next;
		});
	};

	const handleWorkspaceClick = (workspaceId: bigint) => {
		navigate(`/dashboard?workspace=${workspaceId}`);
	};

	const handleAppClick = (appId: bigint) => {
		navigate(`/app/${appId}`);
	};

	return (
		<Sidebar className="border-r-2 border-border">
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

				{/* Orgs & Workspaces */}
				<SidebarGroup>
					<SidebarGroupLabel>Workspaces</SidebarGroupLabel>
					<OrgsSidebar
						orgs={orgs}
						expandedOrgs={expandedOrgs}
						onExpandOrg={toggleOrgExpansion}
						onWorkspaceClick={handleWorkspaceClick}
						onWorkspaceNameChange={setActiveWorkspaceName}
						activeWorkspaceId={activeWorkspaceId}
					/>
				</SidebarGroup>

				{/* Apps in Active Workspace */}
				<SidebarGroup>
					<div className="flex items-center justify-between">
						<div className="flex items-center gap-1">
							<SidebarGroupLabel className="m-0">Apps</SidebarGroupLabel>
							{activeWorkspaceId && (
								<span className="text-xs opacity-60 leading-none">
									In Workspace: {activeWorkspaceName}
								</span>
							)}
						</div>
						{activeWorkspaceId && (
							<button
								onClick={() => navigate("/create-app")}
								className="p-1 hover:bg-secondary-background rounded-neo"
								title="Create App"
							>
								<Plus className="h-4 w-4" />
							</button>
						)}
					</div>
					<SidebarMenu className="space-y-1">
						{activeWorkspaceId ? (
							appsQuery.isLoading ? (
								<>
									<Skeleton className="h-8 w-full rounded-neo" />
									<Skeleton className="h-8 w-full rounded-neo" />
								</>
							) : appsQuery.data?.apps && appsQuery.data.apps.length > 0 ? (
								appsQuery.data.apps.map((app) => (
									<SidebarMenuItem key={app.id.toString()}>
										<SidebarMenuButton
											asChild
											onClick={() => handleAppClick(app.id)}
										>
											<button className="flex items-center gap-2 text-sm">
												<Grid className="h-4 w-4 shrink-0" />
												<span className="truncate">{app.name}</span>
											</button>
										</SidebarMenuButton>
									</SidebarMenuItem>
								))
							) : (
								<p className="text-xs text-foreground opacity-50 px-3 py-2">
									No apps yet
								</p>
							)
						) : (
							<p className="text-xs text-foreground opacity-50 px-3 py-2">
								Select a workspace to view apps
							</p>
						)}
					</SidebarMenu>
				</SidebarGroup>
			</SidebarContent>

			{/* Footer with User Menu & Theme Toggle */}
			<SidebarFooter className="border-t-2 border-border">
				<ThemeToggle />

				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<button className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-neo hover:bg-secondary-background transition-colors">
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
						<DropdownMenuLabel className="font-heading">
							{user?.name}
						</DropdownMenuLabel>
						<DropdownMenuSeparator />
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
