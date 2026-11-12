import { useQuery } from "@connectrpc/connect-query";
import {
	BookOpenIcon,
	Building2Icon,
	ChevronsUpDownIcon,
	KeyRoundIcon,
	LifeBuoyIcon,
	LinkIcon,
	LogOutIcon,
	ArrowUpRightIcon,
	PlusIcon,
	Settings2Icon,
	UserIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";

import { useAuth } from "@/auth/AuthProvider";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/design/Avatar";
import { LocoLogo } from "@/components/design/LocoLogo";
import { ThemeSwatch } from "@/components/design/ThemePreview";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
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
import { expandOrgLink } from "@/lib/org-links";
import { colorThemes, modes, nextMode, parseColorTheme, type Mode } from "@/lib/theme";
import { userLabel } from "@/lib/user";
import { cn } from "@/lib/utils";
import { useTheme } from "@/lib/use-theme";
import { accountSettingsPath, orgSettingsPath, workspacePath } from "@/lib/routes";

import { CreateScopeDialog, type ScopeKind } from "./CreateScopeDialog";
import { useNavSections } from "./nav";
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
	const navigate = useNavigate();
	const { user, logout } = useAuth();
	const { theme, mode, toggle, color, setColor } = useTheme();
	const next = nextMode(mode);
	const dark = theme === "dark";
	const colorName = colorThemes.find((t) => t.id === color)?.name ?? "";
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const [creating, setCreating] = useState<ScopeKind | null>(null);
	const { data: config } = useQuery(getConfig, {});

	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const wsBase = hasWs ? workspacePath(activeOrgId, activeWorkspaceId) : null;

	const orgLinks: NavItem[] =
		activeOrgId !== null
			? (config?.orgLinks ?? []).map((link) => ({
					name: link.label,
					icon: <LinkIcon />,
					href: expandOrgLink(link.url, activeOrgId),
					active: false,
				}))
			: [];

	const sections: { label?: string | undefined; items: NavItem[] }[] = useNavSections().map((section) => ({
		label: section.label,
		items: section.label === "Manage" ? [...section.pages, ...orgLinks] : section.pages,
	}));

	const displayName = user === null ? "" : userLabel(user);

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
									void navigate(accountSettingsPath(activeOrgId, activeWorkspaceId));
								}}
							>
								Account
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
							{activeOrgId !== null && (
								<MenuLink
									icon={<Settings2Icon />}
									onClick={() => {
										void navigate(orgSettingsPath(activeOrgId, activeWorkspaceId));
									}}
								>
									Organization settings
								</MenuLink>
							)}
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
							<MenuLink icon={<ThemeIcon mode={MODE_ICONS[next]} />} onClick={toggle} closeOnClick={false}>
								{`${modes.find((m) => m.id === next)?.name ?? ""} mode`}
							</MenuLink>
							<DropdownMenuSub>
								<DropdownMenuSubTrigger className={cn(menuItem, "gap-2 [&>svg:last-child]:ml-0")}>
									<span className="flex-1">Color theme</span>
									<span className="flex items-center gap-2 text-[13px] text-fg3">
										<ThemeSwatch color={color} dark={dark} />
										{colorName}
									</span>
								</DropdownMenuSubTrigger>
								<DropdownMenuSubContent className="w-[224px]">
									<DropdownMenuRadioGroup
										value={color}
										onValueChange={(value: string) => {
											setColor(parseColorTheme(value));
										}}
									>
										{colorThemes.map((t) => (
											<DropdownMenuRadioItem key={t.id} value={t.id}>
												<ThemeSwatch color={t.id} dark={dark} />
												{t.name}
											</DropdownMenuRadioItem>
										))}
									</DropdownMenuRadioGroup>
								</DropdownMenuSubContent>
							</DropdownMenuSub>
						</MenuGroup>
						<MenuSeparator />
						<MenuGroup>
							<MenuLink
								icon={<LogOutIcon />}
								destructive
								onClick={() => {
									void logout().then(async () => {
										await navigate("/");
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
const MODE_ICONS: Record<Mode, "sun" | "moon" | "monitor"> = { light: "sun", dark: "moon", system: "monitor" };

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
