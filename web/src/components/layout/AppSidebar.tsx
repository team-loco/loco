import { ChevronsUpDown, Home } from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";

import { NavUser } from "@/components/nav-user";
import { NavWorkspaces } from "@/components/nav-workspaces";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
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
	const [selectedOrgId, setSelectedOrgId] = useState<bigint | null>(null);

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
		const workspaceName = workspaces.find(
			(ws) => ws.id === activeWorkspaceId
		)?.name;

		if (activeAppId && appName && workspaceName) {
			setHeader(
				<div className="flex flex-col">
					<h1 className="text-2xl font-mono">
						workspaces::{workspaceName}::app::{appName}
					</h1>
				</div>
			);
		} else if (activeWorkspaceId && workspaceName) {
			setHeader(
				<div className="flex flex-col">
					<h1 className="text-2xl font-mono">workspaces::{workspaceName}</h1>
				</div>
			);
		} else {
			setHeader(
				<div className="flex flex-col">
					<h1 className="text-2xl font-mono">Dashboard</h1>
				</div>
			);
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [
		activeAppId,
		activeWorkspaceId,
		appsQuery.data,
		setHeader,
		workspacesRes,
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

	const activeOrg = orgs.find(
		(org) => org.id === (selectedOrgId || orgs[0]?.id)
	);

	const workspacesData = workspaces.map((ws) => ({
		id: ws.id,
		name: ws.name,
		isActive: activeWorkspaceId === ws.id && !activeAppId,
		hasApps: true,
		apps:
			activeWorkspaceId === ws.id && appsQuery.data?.apps
				? appsQuery.data.apps.map((app) => ({
						id: app.id,
						name: app.name,
				  }))
				: undefined,
	}));

	const navigationItems = [
		{
			title: "Dashboard",
			url: "/dashboard",
			icon: Home,
			isActive: !activeAppId && !activeWorkspaceId,
		},
	];

	return (
		<Sidebar>
			<SidebarHeader className="pt-16">
				<SidebarMenu>
					<SidebarMenuItem>
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<SidebarMenuButton
									size="lg"
									className="bg-sidebar-accent text-sidebar-accent-foreground data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
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
										isActive={org.id === (selectedOrgId || orgs[0]?.id)}
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
				{/* Dashboard */}
				<SidebarGroup>
					<SidebarMenu>
						{navigationItems.map((item) => (
							<SidebarMenuItem key={item.title}>
								<SidebarMenuButton
									onClick={() => navigate(item.url)}
									isActive={item.isActive}
									tooltip={item.title}
								>
									<item.icon className="h-4 w-4" />
									<span>{item.title}</span>
								</SidebarMenuButton>
							</SidebarMenuItem>
						))}
					</SidebarMenu>
				</SidebarGroup>

				{/* Workspaces */}
				{workspaces.length === 0 ? (
					<SidebarGroup>
						<Skeleton className="h-8 w-full rounded-lg" />
						<Skeleton className="h-8 w-full rounded-lg" />
					</SidebarGroup>
				) : (
					<NavWorkspaces
						workspaces={workspacesData}
						activeAppId={activeAppId}
						onWorkspaceClick={(workspaceId) =>
							navigate(`/dashboard?workspace=${workspaceId}`)
						}
						onAppClick={(appId, workspaceId) =>
							navigate(`/app/${appId}?workspace=${workspaceId}`)
						}
						onCreateApp={(workspaceId) =>
							navigate("/create-app", {
								state: { workspaceId },
							})
						}
					/>
				)}
			</SidebarContent>

			<SidebarFooter className="border-t flex flex-col gap-0">
				<div className="border-b pb-2 mb-2">
					<ThemeToggle />
				</div>
				<NavUser
					user={{
						name: user?.name || "User",
						email: user?.email || "",
						avatar: user?.avatarUrl || "",
					}}
					onSettings={() => navigate("/profile")}
					onLogout={handleLogout}
				/>
			</SidebarFooter>
		</Sidebar>
	);
}
