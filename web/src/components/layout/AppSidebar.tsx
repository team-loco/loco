import {
	Activity,
	BookOpen,
	Calendar,
	CheckCircle,
	Home,
	Key,
	Package,
	TrendingUp,
	Users,
	Zap,
} from "lucide-react";
import * as React from "react";

import { NavUser } from "@/components/nav-user";
import { Badge } from "@/components/ui/badge";
import {
	Sidebar,
	SidebarContent,
	SidebarGroup,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarMenuSub,
	SidebarMenuSubButton,
	SidebarMenuSubItem,
	SidebarRail,
	useSidebar,
} from "@/components/ui/sidebar";
import { listUserOrgs } from "@/gen/org/v1";
import { whoAmI } from "@/gen/user/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
import { useQuery } from "@connectrpc/connect-query";
import { useLocation, useNavigate } from "react-router";
import { ThemeToggle } from "./ThemeToggle";

type NavItemBase = {
	title: string;
	url: string;
	icon: React.ComponentType<{ className?: string }>;
};

type SectionNavItem = {
	section: string;
	items: Array<NavItemBase & { badge?: string }>;
};

type RegularNavItem = NavItemBase & {
	items: Array<{ title: string; url: string }>;
};

const data = {
	navMain: [
		{
			title: "Dashboard",
			url: "/dashboard",
			icon: Home,
			items: [],
		},
		{
			title: "Resources",
			url: "/resources",
			icon: Package,
			items: [],
		},
		{
			title: "Observability",
			url: "/observability",
			icon: Activity,
			items: [],
		},
		{
			title: "Events",
			url: "/events",
			icon: Calendar,
			items: [],
		},
		{
			title: "Usage",
			url: "/usage",
			icon: TrendingUp,
			items: [],
		},
		{
			title: "Tokens",
			url: "/tokens",
			icon: Key,
			items: [],
		},
		{
			title: "Team",
			url: "/team",
			icon: Users,
			items: [],
		},
		{
			section: "Resources",
			items: [
				{
					title: "Docs",
					url: "/docs",
					icon: BookOpen,
				},
				{
					title: "Packages",
					url: "/packages",
					icon: Package,
					badge: "Coming Soon",
				},
				{
					title: "Status Page",
					url: "#",
					icon: CheckCircle,
					badge: "Coming Soon",
				},
			],
		},
	],
};

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
	const navigate = useNavigate();
	const location = useLocation();
	const { toggleSidebar } = useSidebar();

	const { data: whoAmIResponse } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		user ? { userId: user.id } : undefined,
		{ enabled: !!user }
	);

	const orgs = orgsRes?.orgs ?? [];
	const firstOrgId = orgs[0]?.id ?? null;

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);
	const workspaces = workspacesRes?.workspaces ?? [];

	const activeWorkspaceId = new URLSearchParams(location.search).get(
		"workspace"
	)
		? BigInt(new URLSearchParams(location.search).get("workspace") || "0")
		: null;

	const isActive = (url: string) => {
		if (url === "#") return false;
		return location.pathname === url || location.pathname.startsWith(url + "/");
	};

	React.useEffect(() => {
		const handleKeyDown = (e: KeyboardEvent) => {
			const isMac = /Mac|iPhone|iPad|iPod/.test(navigator.platform);
			const isToggleKey = e.key === "b" || e.key === "B";
			const isCorrectModifier = isMac ? e.metaKey : e.ctrlKey;

			if (isToggleKey && isCorrectModifier && !e.shiftKey && !e.altKey) {
				e.preventDefault();
				toggleSidebar();
			}
		};

		window.addEventListener("keydown", handleKeyDown);
		return () => window.removeEventListener("keydown", handleKeyDown);
	}, [toggleSidebar]);

	return (
		<Sidebar {...props}>
			<SidebarHeader className="pt-14">
				<SidebarMenu>
					<SidebarMenuItem>
						<SidebarMenuButton
							size="lg"
							asChild
							className="hover:bg-transparent active:bg-transparent focus-visible:bg-transparent data-[state=open]:bg-transparent"
						>
							<a
								href="/dashboard"
								onClick={(e) => e.preventDefault()}
								className="cursor-default"
							>
								<div className="bg-sidebar-primary text-sidebar-primary-foreground flex aspect-square size-8 items-center justify-center rounded-lg">
									<Zap className="size-4" />
								</div>
								<div className="flex flex-col gap-0.5 leading-none">
									<span className="font-medium">Loco</span>
									<span className="text-xs">Deploy & Scale</span>
								</div>
							</a>
						</SidebarMenuButton>
					</SidebarMenuItem>
				</SidebarMenu>
			</SidebarHeader>

			<SidebarContent>
				{data.navMain.map((item, idx) => {
					if ("section" in item) {
						const sectionItem = item as SectionNavItem;
						return (
							<SidebarGroup key={sectionItem.section}>
								<SidebarGroupLabel>{sectionItem.section}</SidebarGroupLabel>
								<SidebarMenu>
									{sectionItem.items.map(
										(subItem: (typeof sectionItem.items)[0]) => (
											<SidebarMenuItem key={subItem.title}>
												<SidebarMenuButton
													onClick={() => {
														if (!subItem.badge) {
															navigate(subItem.url);
														}
													}}
													isActive={isActive(subItem.url)}
													className={`flex items-center justify-between ${
														subItem.badge
															? "cursor-not-allowed opacity-60 hover:bg-transparent"
															: "cursor-pointer"
													}`}
												>
													<div className="flex items-center gap-2">
														<subItem.icon className="size-4" />
														<span>{subItem.title}</span>
													</div>
													{subItem.badge && (
														<Badge className="bg-yellow-500 border-0 text-xs font-mono">
															{subItem.badge}
														</Badge>
													)}
												</SidebarMenuButton>
											</SidebarMenuItem>
										)
									)}
								</SidebarMenu>
							</SidebarGroup>
						);
					}

					const navItem = item as RegularNavItem;
					return (
						<SidebarGroup key={navItem.title || idx}>
							<SidebarMenu>
								<SidebarMenuItem>
									<SidebarMenuButton
										onClick={() => navigate(navItem.url)}
										isActive={isActive(navItem.url)}
										className={`${
											(navItem.items ?? []).length ? "" : "font-medium"
										} cursor-pointer`}
										tooltip={navItem.title}
									>
										<navItem.icon className="size-4" />
										<span>{navItem.title}</span>
									</SidebarMenuButton>

									{(navItem.items ?? []).length > 0 ? (
										<SidebarMenuSub>
											{navItem.items?.map(
												(navSubItem: (typeof navItem.items)[0]) => (
													<SidebarMenuSubItem key={navSubItem.title}>
														<SidebarMenuSubButton
															onClick={() => navigate(navSubItem.url)}
															isActive={isActive(navSubItem.url)}
															className="cursor-pointer"
														>
															<span>{navSubItem.title}</span>
														</SidebarMenuSubButton>
													</SidebarMenuSubItem>
												)
											)}
										</SidebarMenuSub>
									) : null}
								</SidebarMenuItem>
							</SidebarMenu>
						</SidebarGroup>
					);
				})}
			</SidebarContent>

			<SidebarGroup className="mt-auto border-t ">
				<div className="border-b pb-1 pt-1">
					<ThemeToggle />
				</div>
				<NavUser
					user={{
						name: user?.name || "User",
						email: user?.email || "",
						avatar: user?.avatarUrl || "",
					}} 
					workspaces={workspaces.map((ws) => ({
						id: ws.id,
						name: ws.name,
					}))}
					activeWorkspaceId={activeWorkspaceId}
				/>
			</SidebarGroup>

			<SidebarRail />
		</Sidebar>
	);
}
