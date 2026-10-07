import {
	BellIcon,
	BookOpenIcon,
	Building2Icon,
	ChartLineIcon,
	ChevronsUpDownIcon,
	KeyRoundIcon,
	LayoutDashboardIcon,
	LifeBuoyIcon,
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

import { useAuth } from "@/auth/AuthProvider";
import { LocoLogo } from "@/components/design/LocoLogo";
import { SoonTag } from "@/components/design/SoonTag";
import {
	DropdownMenu,
	DropdownMenuContent,
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
import { cn } from "@/lib/utils";
import { useTheme } from "@/lib/use-theme";
import { observabilityPath, workspacePath, type ObservabilityView } from "@/lib/routes";

import { useSidebarPeek } from "./AppShell";
import { CreateScopeDialog, type ScopeKind } from "./CreateScopeDialog";
import { playThemeSound, ThemeIcon } from "./ThemeIcon";

interface NavItem {
	name: string;
	icon: ReactNode;
	to?: string | undefined;
	active: boolean;
	soon?: boolean;
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
	const { theme, toggleTheme } = useTheme();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const [creating, setCreating] = useState<ScopeKind | null>(null);
	const peek = useSidebarPeek();


	const env = params.get("env");
	const envParams: Record<string, string> = env !== null ? { env } : {};
	const view = params.get("view") ?? "logs";
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const wsBase = hasWs ? workspacePath(activeOrgId, activeWorkspaceId) : null;
	const onObs = wsBase !== null && pathname.startsWith(`${wsBase}/observability`);

	const obs = (v: ObservabilityView): string | undefined =>
		hasWs ? observabilityPath(activeOrgId, activeWorkspaceId, v, envParams) : undefined;
	const withEnv = (path: string) => (env !== null ? `${path}?env=${encodeURIComponent(env)}` : path);

	const sections: { label?: string; items: NavItem[] }[] = [
		{
			items: [
				{
					name: "Dashboard",
					icon: <LayoutDashboardIcon />,
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
				{ name: "Traces", icon: <WaypointsIcon />, active: false, soon: true },
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
			],
		},
	];

	const displayName = user?.name !== undefined && user.name !== "" ? user.name : (user?.email ?? "");

	return (
		<Sidebar collapsible="icon" onMouseEnter={peek.onEnter} onMouseLeave={peek.onLeave}>
			<SidebarHeader className="h-[52px] shrink-0 items-center justify-center border-b border-line px-5 py-0">
				<Link
					to={wsBase ?? "/dashboard"}
					aria-label="Loco dashboard"
					className="flex w-fit items-center rounded-sm outline-none [--logo:var(--logo-sidebar)] focus-visible:ring-2 focus-visible:ring-sidebar-ring"
				>
					<LocoLogo className="w-[46px] transition-opacity duration-150 group-data-[collapsible=icon]:opacity-0" />
				</Link>
			</SidebarHeader>

			<SidebarContent className="gap-0 py-3">
				{sections.map((section) => (
					<SidebarGroup key={section.label ?? "root"} className="px-3 py-0">
						{section.label !== undefined && <SidebarGroupLabel>{section.label}</SidebarGroupLabel>}
						<SidebarMenu className="gap-0.5">
							{section.items.map((item) => (
								<SidebarMenuItem key={item.name}>
									{item.to !== undefined && item.soon !== true ? (
										<SidebarMenuButton
											isActive={item.active}
											tooltip={item.name}
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
											tooltip={item.soon === true ? `${item.name} (soon)` : item.name}
											className="text-fg4 aria-disabled:pointer-events-auto aria-disabled:cursor-not-allowed aria-disabled:opacity-100 hover:bg-transparent hover:text-fg4 [&_svg]:text-fg4"
										>
											{item.icon}
											<span className="flex-1">{item.name}</span>
											{item.soon === true && <SoonTag />}
										</SidebarMenuButton>
									)}
								</SidebarMenuItem>
							))}
						</SidebarMenu>
					</SidebarGroup>
				))}
			</SidebarContent>

			<SidebarFooter className="border-t border-line p-2">
				<SidebarMenu>
					<SidebarMenuItem>
						<DropdownMenu onOpenChange={peek.lock}>
							<DropdownMenuTrigger
								render={
									<SidebarMenuButton
										size="lg"
										className="h-[44px] gap-2.5 text-foreground hover:bg-bg3 hover:text-foreground aria-expanded:bg-bg3"
									/>
								}
							>
								<UserAvatar
									name={displayName}
									src={user?.avatarUrl}
									className="size-[28px] rounded-full border border-line bg-bg3 text-xs text-fg2"
								/>
								<span className="flex min-w-0 flex-1 flex-col leading-tight">
									<span className="truncate text-md font-semibold">{displayName}</span>
									<span className="truncate text-sm text-fg3">{user?.email ?? ""}</span>
								</span>
								<ChevronsUpDownIcon className="text-fg3" />
							</DropdownMenuTrigger>
							<DropdownMenuContent className="w-[264px]" side="top" align="start">
								<div className="flex items-center gap-2.5 px-2.5 py-2">
									<UserAvatar name={displayName} src={user?.avatarUrl} />
									<span className="flex min-w-0 flex-col leading-tight">
										<span className="truncate text-md font-semibold">{displayName}</span>
										<span className="truncate text-sm text-fg3">{user?.email ?? ""}</span>
									</span>
								</div>
								<DropdownMenuSeparator />
								<MenuLink icon={<UserIcon />} onClick={() => { void navigate("/profile"); }}>Profile</MenuLink>
								<MenuLink icon={<KeyRoundIcon />} onClick={() => { void navigate("/tokens?owner=personal"); }}>
									Personal access tokens
								</MenuLink>
								<DropdownMenuSeparator />
								<MenuLink icon={<Building2Icon />} onClick={() => { void navigate("/organizations"); }}>
									Organizations
								</MenuLink>
								<MenuLink icon={<PlusIcon />} onClick={() => { setCreating("org"); }}>
									New organization
								</MenuLink>
								<DropdownMenuSeparator />
								<ExternalMenuLink icon={<BookOpenIcon />} href="https://github.com/team-loco/loco">
									Documentation
								</ExternalMenuLink>
								<ExternalMenuLink icon={<LifeBuoyIcon />} href="https://github.com/team-loco/loco/issues">
									Support
								</ExternalMenuLink>
								<DropdownMenuSeparator />
								<MenuLink
									icon={<ThemeIcon mode={theme === "dark" ? "sun" : "moon"} />}
									onClick={() => {
										const toDark = theme !== "dark";
										toggleTheme();
										void playThemeSound(toDark);
									}}
									closeOnClick={false}
								>
									{theme === "dark" ? "Light mode" : "Dark mode"}
								</MenuLink>
								<DropdownMenuSeparator />
								<MenuLink
									icon={<LogOutIcon />}
									destructive
									onClick={() => {
										void logout().then(async () => { await navigate("/login"); });
									}}
								>
									Sign out
								</MenuLink>
							</DropdownMenuContent>
						</DropdownMenu>
					</SidebarMenuItem>
				</SidebarMenu>
			</SidebarFooter>

			<CreateScopeDialog
				kind={creating}
				onOpenChange={(open) => { if (!open) setCreating(null); }}
			/>
		</Sidebar>
	);
}

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
			className="justify-between"
		>
			{children}
			<span className="flex text-fg3 [&_svg]:size-[15px]">{icon}</span>
		</DropdownMenuItem>
	);
}

function ExternalMenuLink({ icon, href, children }: { icon: ReactNode; href: string; children: ReactNode }) {
	return (
		<DropdownMenuItem
			render={<a href={href} target="_blank" rel="noopener noreferrer" />}
			className="justify-between"
		>
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
		<span
			className={cn(
				"flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-line text-sm font-semibold text-foreground",
				className
			)}
		>
			{src !== undefined && src !== "" ? <img src={src} alt="" className="size-full object-cover" /> : initials(name)}
		</span>
	);
}
