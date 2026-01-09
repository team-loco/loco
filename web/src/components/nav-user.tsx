import {
	BadgeCheck,
	Bell,
	ChevronsUpDown,
	CreditCard,
	LogOut,
	Check,
	HelpCircle,
	Building2,
	Settings,
	Plus,
} from "lucide-react";
import { useNavigate, useSearchParams } from "react-router";
import { toastConnectError } from "@/lib/error-handler";
import { useOrgContext } from "@/hooks/useOrgContext";
import type { Organization } from "@/gen/org/v1/org_pb";
import { CreateOrgDialog } from "@/components/org/CreateOrgDialog";
import { CreateWorkspaceDialog } from "@/components/workspace/CreateWorkspaceDialog";
import { useState } from "react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	useSidebar,
} from "@/components/ui/sidebar";
import { useAuth } from "@/auth/AuthProvider";
import { toast } from "sonner";
import { useTheme } from "@/lib/use-theme";
import "./layout/ThemeToggle.css";

export interface Workspace {
	id: bigint;
	name: string;
}

export function NavUser({
	user,
	workspaces = [],
	activeWorkspaceId,
	orgs = [],
}: {
	user: {
		name: string;
		email: string;
		avatar: string;
	};
	workspaces?: Workspace[];
	activeWorkspaceId?: bigint | null;
	orgs?: Organization[];
}) {
	const { isMobile } = useSidebar();
	const navigate = useNavigate();
	const { logout } = useAuth();
	const [searchParams] = useSearchParams();
	const { activeOrgId, setActiveOrgId } = useOrgContext(orgs.map((o) => o.id));
	const { theme, toggleTheme } = useTheme();

	const activeOrg = orgs.find((org) => org.id === activeOrgId);

	const playSound = async (isDark: boolean) => {
		new window.AudioContext(); // necessary fix audio delay on Safari
		const audio = new Audio(`${isDark ? "/lightMode.wav" : "/darkMode.wav"}`);
		audio.volume = 0.9;
		await audio.play();
	};

	const handleThemeToggle = async () => {
		const isDark = theme === "dark";
		toggleTheme();
		await playSound(isDark);
	};

	// Dialog state
	const [createOrgOpen, setCreateOrgOpen] = useState(false);
	const [createWorkspaceOpen, setCreateWorkspaceOpen] = useState(false);

	const handleOrgSwitch = (orgId: bigint) => {
		if (orgId === activeOrgId) return;
		setActiveOrgId(orgId);
		// Navigate to dashboard with new org context
		navigate(`/dashboard?org=${orgId}`);
	};

	const handleWorkspaceSwitch = (workspaceId: bigint) => {
		// Preserve org context when switching workspaces
		const orgParam = searchParams.get("org");
		const url = orgParam
			? `/dashboard?org=${orgParam}&workspace=${workspaceId}`
			: `/dashboard?workspace=${workspaceId}`;
		navigate(url);
	};

	const handleCreateOrgSuccess = (orgId: bigint) => {
		// Switch to the new org
		setActiveOrgId(orgId);
		navigate(`/dashboard?org=${orgId}`);
	};

	const handleCreateWorkspaceSuccess = (workspaceId: bigint) => {
		// Switch to the new workspace
		const orgParam = searchParams.get("org");
		const url = orgParam
			? `/dashboard?org=${orgParam}&workspace=${workspaceId}`
			: `/dashboard?workspace=${workspaceId}`;
		navigate(url);
	};

	return (
		<SidebarMenu>
			<SidebarMenuItem>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<SidebarMenuButton
							size="lg"
							className="data-[state=open]:bg-sidebar-accent/90 cursor-pointer"
						>
							<Avatar className="h-8 w-8 rounded-lg">
								<AvatarImage src={user.avatar} alt={user.name} />
								<AvatarFallback className="rounded-lg">CN</AvatarFallback>
							</Avatar>
							<div className="grid flex-1 text-left text-sm leading-tight">
								<span className="truncate font-semibold">{user.name}</span>
								<span className="truncate text-xs">{user.email}</span>
							</div>
							<ChevronsUpDown className="ml-auto size-4" />
						</SidebarMenuButton>
					</DropdownMenuTrigger>
					<DropdownMenuContent
						className="w-[--radix-dropdown-menu-trigger-width] min-w-56 rounded-lg"
						side={isMobile ? "bottom" : "right"}
						align="end"
						sideOffset={4}
					>
						<DropdownMenuLabel className="p-0 font-normal">
							<div className="px-1 py-1.5 text-left text-sm">
								<span className="truncate font-bold">{user.name}</span>
							</div>
						</DropdownMenuLabel>
						<DropdownMenuSeparator />

						{/* Organization Switcher */}
						{orgs.length > 0 && (
							<>
								<DropdownMenuLabel className="px-2 py-1.5 text-xs font-semibold text-muted-foreground">
									Organization
								</DropdownMenuLabel>
								<DropdownMenuGroup>
									{orgs.map((org) => (
										<DropdownMenuItem
											key={org.id.toString()}
											onClick={() => handleOrgSwitch(org.id)}
											className="flex items-center justify-between gap-2 cursor-pointer"
										>
											<div className="flex items-center gap-2 flex-1 min-w-0">
												<Building2 className="size-4 shrink-0 text-muted-foreground" />
												<span className="truncate">{org.name}</span>
											</div>
											<div className="flex items-center gap-1 shrink-0">
												{activeOrgId === org.id && (
													<Check className="size-4 text-primary" />
												)}
												<button
													onClick={(e) => {
														e.stopPropagation();
														navigate(`/org/${org.id}/settings`);
													}}
													className="p-1 hover:bg-accent rounded-sm transition-colors cursor-pointer"
													aria-label="Organization settings"
												>
													<Settings className="size-3 text-muted-foreground hover:text-foreground" />
												</button>
											</div>
										</DropdownMenuItem>
									))}
								</DropdownMenuGroup>

								{/* Create Org Button */}
								<DropdownMenuItem
									onClick={() => setCreateOrgOpen(true)}
									className="cursor-pointer text-primary"
								>
									<Plus className="size-4" />
									<span>Create Organization</span>
								</DropdownMenuItem>

								<DropdownMenuSeparator />
							</>
						)}

						{/* Workspace Switcher */}
						{workspaces.length > 0 && (
							<>
								<DropdownMenuLabel className="px-2 py-1.5 text-xs font-semibold text-muted-foreground">
									Workspaces{activeOrg ? ` (${activeOrg.name})` : ""}
								</DropdownMenuLabel>
								<DropdownMenuGroup>
									{workspaces.map((workspace) => (
										<DropdownMenuItem
											key={workspace.id.toString()}
											onClick={() => handleWorkspaceSwitch(workspace.id)}
											className="flex items-center justify-between cursor-pointer"
										>
											<span>{workspace.name}</span>
											{activeWorkspaceId === workspace.id && (
												<Check className="h-4 w-4" />
											)}
										</DropdownMenuItem>
									))}
								</DropdownMenuGroup>

								{/* Create Workspace Button */}
								<DropdownMenuItem
									onClick={() => setCreateWorkspaceOpen(true)}
									className="cursor-pointer text-primary"
								>
									<Plus className="size-4" />
									<span>Create Workspace</span>
								</DropdownMenuItem>

								<DropdownMenuSeparator />
							</>
						)}

						<DropdownMenuGroup>
							<DropdownMenuItem onClick={() => navigate("/profile")} className="cursor-pointer">
								<BadgeCheck />
								Account
							</DropdownMenuItem>
							<DropdownMenuItem className="cursor-pointer">
								<CreditCard />
								Billing
							</DropdownMenuItem>
							<DropdownMenuItem className="cursor-pointer">
								<Bell />
								Notifications
							</DropdownMenuItem>
							<DropdownMenuItem className="cursor-pointer">
								<HelpCircle />
								Support
							</DropdownMenuItem>
							<DropdownMenuItem
								onClick={(e) => {
									e.preventDefault();
									void handleThemeToggle();
								}}
								onSelect={(e) => e.preventDefault()}
								className="cursor-pointer"
							>
								{theme === "dark" ? (
									<div className="div-toggle-btn-dark border-0 shadow-none h-4 w-4"></div>
								) : (
									<div className="div-toggle-btn-light border-0 shadow-none h-4 w-4"></div>
								)}
								{theme === "dark" ? "Light Mode" : "Dark Mode"}
							</DropdownMenuItem>
						</DropdownMenuGroup>
						<DropdownMenuSeparator />
						<DropdownMenuItem
							onClick={async () => {
								try {
									await logout();
									navigate("/");
									toast.success("Logged out successfully");
								} catch (error) {
									toastConnectError(error, "Failed to logout");
								}
							}}
							className="cursor-pointer"
						>
							<LogOut />
							Log out
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</SidebarMenuItem>

			{/* Dialogs */}
			<CreateOrgDialog
				open={createOrgOpen}
				onOpenChange={setCreateOrgOpen}
				onSuccess={handleCreateOrgSuccess}
			/>
			{activeOrgId && (
				<CreateWorkspaceDialog
					open={createWorkspaceOpen}
					onOpenChange={setCreateWorkspaceOpen}
					orgId={activeOrgId}
					onSuccess={handleCreateWorkspaceSuccess}
				/>
			)}
		</SidebarMenu>
	);
}
