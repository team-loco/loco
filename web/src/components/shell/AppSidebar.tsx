import { useQuery } from "@connectrpc/connect-query";
import {
	BellIcon,
	BookOpenIcon,
	Building2Icon,
	ChartLineIcon,
	ChevronsUpDownIcon,
	KeyRoundIcon,
	LayoutGridIcon,
	LifeBuoyIcon,
	LinkIcon,
	LogOutIcon,
	ArrowUpRightIcon,
	PlusIcon,
	ScrollTextIcon,
	Settings2Icon,
	UserIcon,
	UsersIcon,
	WaypointsIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";

import { useAuth } from "@/auth/AuthProvider";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/design/Avatar";
import { LocoLogo } from "@/components/design/LocoLogo";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
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
} from "@/components/design/Sidebar";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { pageImporters } from "@/lib/lazy-pages";
import { expandOrgLink } from "@/lib/org-links";
import { cn } from "@/lib/utils";
import { useTheme } from "@/lib/use-theme";
import { newServicePath, observabilityPath, workspacePath, type ObservabilityView } from "@/lib/routes";

import { CreateScopeDialog, type ScopeKind } from "./CreateScopeDialog";
import { SidebarToggle } from "./SidebarToggle";
import { ThemeIcon } from "./ThemeIcon";

interface NavItem {
	name: string;
	icon: ReactNode;
	to?: string | undefined;
	href?: string;
	active: boolean;
	preload?: (() => Promise<unknown>) | undefined;
}

const NAME_SEPARATORS = /[\s._-]+/;

function initials(name: string): string {
	const parts = name.trim().split(NAME_SEPARATORS).filter(Boolean);
	const first = parts[0]?.[0] ?? "?";
	const second = parts[1]?.[0] ?? "";
	return (first + second).toUpperCase();
}

function preloadPage(load: (() => Promise<unknown>) | undefined) {
	if (load === undefined) return;
	load().catch(() => undefined);
}

export function AppSidebar() {
	const { pathname } = useLocation();
	const [params] = useSearchParams();
	const navigate = useNavigate();
	const { user, logout } = useAuth();
	const { theme, toggle } = useTheme();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const [creating, setCreating] = useState<ScopeKind | null>(null);
	const { data: config } = useQuery(getConfig, {});

	const env = params.get("env");
	const envParams: Record<string, string> = env !== null ? { env } : {};
	const view = params.get("view") ?? "logs";
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const wsBase = hasWs ? workspacePath(activeOrgId, activeWorkspaceId) : null;
	const onObs = wsBase !== null && pathname.startsWith(`${wsBase}/observability`);

	const obs = (v: ObservabilityView): string | undefined =>
		hasWs ? observabilityPath(activeOrgId, activeWorkspaceId, v, envParams) : undefined;
	const withEnv = (path: string) => (env !== null ? `${path}?env=${encodeURIComponent(env)}` : path);
	const orgLinks: NavItem[] =
		activeOrgId !== null
			? (config?.orgLinks ?? []).map((link) => ({
					name: link.label,
					icon: <LinkIcon />,
					href: expandOrgLink(link.url, activeOrgId),
					active: false,
				}))
			: [];

	const sections: { label?: string; items: NavItem[] }[] = [
		{
			items: [
				{
					name: "Dashboard",
					icon: <LayoutGridIcon />,
					to: wsBase !== null ? withEnv(wsBase) : undefined,
					active: wsBase !== null && (pathname === wsBase || pathname.startsWith(`${wsBase}/resource`)),
					preload: pageImporters.Dashboard,
				},
			],
		},
		{
			label: "Observability",
			items: [
				{
					name: "Logs",
					icon: <ScrollTextIcon />,
					to: obs("logs"),
					active: onObs && view === "logs",
					preload: pageImporters.Observability,
				},
				{
					name: "Metrics",
					icon: <ChartLineIcon />,
					to: obs("metrics"),
					active: onObs && view === "metrics",
					preload: pageImporters.Observability,
				},
				{
					name: "Traces",
					icon: <WaypointsIcon />,
					to: obs("traces"),
					active: onObs && view === "traces",
					preload: pageImporters.Observability,
				},
				{
					name: "Events",
					icon: <BellIcon />,
					to: obs("events"),
					active: onObs && view === "events",
					preload: pageImporters.Observability,
				},
			],
		},
		{
			label: "Manage",
			items: [
				{
					name: "Tokens",
					icon: <KeyRoundIcon />,
					to: "/tokens",
					active: pathname === "/tokens",
					preload: pageImporters.Tokens,
				},
				{
					name: "Team",
					icon: <UsersIcon />,
					to: activeOrgId !== null ? `/org/${activeOrgId}/team` : undefined,
					active: pathname.endsWith("/team"),
					preload: pageImporters.Team,
				},
				{
					name: "Settings",
					icon: <Settings2Icon />,
					to: wsBase !== null ? withEnv(`${wsBase}/settings`) : undefined,
					active: pathname.endsWith("/settings"),
					preload: pageImporters.Settings,
				},
				...orgLinks,
			],
		},
	];

	const displayName = user?.name !== undefined && user.name !== "" ? user.name : (user?.email ?? "");

	return (
		<Sidebar>
			<SidebarHeader className="relative mt-[9px] h-14 shrink-0 flex-row items-center justify-center px-12 py-0">
				<Link
					to={wsBase ?? "/dashboard"}
					aria-label="Loco dashboard"
					className="flex h-8 items-center rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
				>
					<LocoLogo className="w-[46px]" />
				</Link>
				<SidebarToggle className="absolute top-3 right-2.5" />
			</SidebarHeader>

			{hasWs && (
				<Link
					to={newServicePath(activeOrgId, activeWorkspaceId, envParams)}
					onMouseEnter={() => {
						preloadPage(pageImporters.Dashboard);
					}}
					className="mx-3 mt-1 flex h-[34px] shrink-0 items-center gap-2 rounded-[7px] border border-line2 bg-raise px-2.5 text-[13.5px] font-medium text-foreground outline-none transition-colors duration-150 hover:border-fg4 focus-visible:ring-2 focus-visible:ring-sidebar-ring dark:hover:border-line2 dark:hover:bg-panel2"
				>
					<PlusIcon className="size-4 text-fg3" />
					New resource
				</Link>
			)}

			<SidebarContent className="gap-0 px-3 pt-4 pb-3">
				{sections.map((section) => (
					<SidebarGroup key={section.label ?? "root"} className="p-0">
						{section.label !== undefined && <SidebarGroupLabel>{section.label}</SidebarGroupLabel>}
						<SidebarMenu className="gap-0.5">
							{section.items.map((item) => (
								<SidebarMenuItem key={item.href === undefined ? item.name : `${item.name} ${item.href}`}>
									{item.href !== undefined ? (
										<SidebarMenuButton render={<a href={item.href} target="_blank" rel="noopener noreferrer" />}>
											{item.icon}
											<span className="flex-1 truncate">{item.name}</span>
											<ArrowUpRightIcon className="text-fg3" />
										</SidebarMenuButton>
									) : item.to !== undefined ? (
										<SidebarMenuButton
											isActive={item.active}
											render={<Link to={item.to} />}
											onMouseEnter={() => {
												preloadPage(item.preload);
											}}
											onFocus={() => {
												preloadPage(item.preload);
											}}
										>
											{item.icon}
											<span>{item.name}</span>
										</SidebarMenuButton>
									) : (
										<SidebarMenuButton
											aria-disabled
											className="text-fg4 aria-disabled:pointer-events-auto aria-disabled:cursor-not-allowed aria-disabled:opacity-100 hover:bg-transparent hover:text-fg4 [&_svg]:text-fg4"
										>
											{item.icon}
											<span className="flex-1">{item.name}</span>
										</SidebarMenuButton>
									)}
								</SidebarMenuItem>
							))}
						</SidebarMenu>
					</SidebarGroup>
				))}
			</SidebarContent>

			<SidebarFooter className="p-3">
				<DropdownMenu>
					<DropdownMenuTrigger className="flex w-full items-center gap-2.5 rounded-md p-1.5 text-left outline-none transition-colors duration-150 hover:bg-shell-hover focus-visible:ring-2 focus-visible:ring-sidebar-ring aria-expanded:bg-shell-hover">
						<UserAvatar name={displayName} src={user?.avatarUrl} className="size-7 text-[10.5px]" />
						<span className="flex min-w-0 flex-1 flex-col leading-tight">
							<span className="truncate text-[13px] font-medium">{displayName}</span>
							<span className="truncate text-[11.5px] text-fg3">{user?.email ?? ""}</span>
						</span>
						<ChevronsUpDownIcon className="size-4 shrink-0 text-fg3" />
					</DropdownMenuTrigger>
					<DropdownMenuContent className="w-[264px] overflow-hidden p-0" side="top" align="start">
						<div className="flex items-center gap-3 px-4 py-3.5">
							<UserAvatar name={displayName} src={user?.avatarUrl} className="size-10 text-[13px]" />
							<span className="flex min-w-0 flex-col">
								<span className="truncate text-[15px] font-semibold">{displayName}</span>
								<span className="truncate text-[13px] text-fg3">{user?.email ?? ""}</span>
							</span>
						</div>
						<MenuSeparator />
						<MenuGroup>
							<MenuLink
								icon={<UserIcon />}
								onClick={() => {
									void navigate("/profile");
								}}
							>
								Profile
							</MenuLink>
							<MenuLink
								icon={<KeyRoundIcon />}
								onClick={() => {
									void navigate("/tokens?owner=personal");
								}}
							>
								Personal access tokens
							</MenuLink>
						</MenuGroup>
						<MenuSeparator />
						<MenuGroup>
							<MenuLink
								icon={<Building2Icon />}
								onClick={() => {
									void navigate("/organizations");
								}}
							>
								Organizations
							</MenuLink>
							<MenuLink
								icon={<PlusIcon />}
								onClick={() => {
									setCreating("org");
								}}
							>
								New organization
							</MenuLink>
						</MenuGroup>
						<MenuSeparator />
						<MenuGroup>
							<ExternalMenuLink icon={<BookOpenIcon />} href="https://github.com/team-loco/loco">
								Documentation
							</ExternalMenuLink>
							<ExternalMenuLink icon={<LifeBuoyIcon />} href="https://github.com/team-loco/loco/issues">
								Support
							</ExternalMenuLink>
						</MenuGroup>
						<MenuSeparator />
						<MenuGroup>
							<MenuLink
								icon={<ThemeIcon mode={theme === "dark" ? "sun" : "moon"} />}
								onClick={toggle}
								closeOnClick={false}
							>
								{theme === "dark" ? "Light mode" : "Dark mode"}
							</MenuLink>
						</MenuGroup>
						<MenuSeparator />
						<MenuGroup>
							<MenuLink
								icon={<LogOutIcon />}
								destructive
								onClick={() => {
									void logout().then(async () => {
										await navigate("/login");
									});
								}}
							>
								Sign out
							</MenuLink>
						</MenuGroup>
					</DropdownMenuContent>
				</DropdownMenu>
			</SidebarFooter>

			<CreateScopeDialog
				kind={creating}
				onOpenChange={(open) => {
					if (!open) setCreating(null);
				}}
			/>
		</Sidebar>
	);
}

function MenuGroup({ children }: { children: ReactNode }) {
	return <DropdownMenuGroup className="py-1">{children}</DropdownMenuGroup>;
}

function MenuSeparator() {
	return <DropdownMenuSeparator className="mx-0 my-0" />;
}

const menuItem = "h-10 justify-between rounded-none px-4 text-[14px]";

function MenuLink({
	icon,
	children,
	onClick,
	destructive,
	closeOnClick,
}: {
	icon: ReactNode;
	children: ReactNode;
	onClick: () => void;
	destructive?: boolean;
	closeOnClick?: boolean;
}) {
	return (
		<DropdownMenuItem
			onClick={onClick}
			closeOnClick={closeOnClick}
			variant={destructive === true ? "destructive" : "default"}
			className={menuItem}
		>
			{children}
			<span className="flex text-fg3 [&_svg]:size-[15px]">{icon}</span>
		</DropdownMenuItem>
	);
}

function ExternalMenuLink({ icon, href, children }: { icon: ReactNode; href: string; children: ReactNode }) {
	return (
		<DropdownMenuItem render={<a href={href} target="_blank" rel="noopener noreferrer" />} className={menuItem}>
			<span className="flex items-center gap-1">
				{children}
				<ArrowUpRightIcon className="size-3 text-fg3" />
			</span>
			<span className="flex text-fg3 [&_svg]:size-[15px]">{icon}</span>
		</DropdownMenuItem>
	);
}

function UserAvatar({ name, src, className }: { name: string; src?: string | undefined; className?: string }) {
	return (
		<Avatar className={cn("rounded-full after:hidden [&_img]:rounded-full", className)}>
			{src !== undefined && src !== "" && <AvatarImage src={src} alt="" />}
			<AvatarFallback className="rounded-full bg-panel2 font-mono text-[length:inherit] font-normal text-fg2">
				{initials(name)}
			</AvatarFallback>
		</Avatar>
	);
}
