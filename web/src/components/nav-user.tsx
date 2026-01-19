import {
	BadgeCheck,
	Bell,
	ChevronsUpDown,
	CreditCard,
	LogOut,
	Check,
	HelpCircle,
	Building2,
	Plus,
} from "lucide-react";
import { useNavigate } from "react-router";
import { toastConnectError } from "@/lib/error-handler";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { Organization } from "@/gen/loco/org/v1/org_pb";
import { CreateOrgDialog } from "@/components/org/CreateOrgDialog";
import { CreateWorkspaceDialog } from "@/components/workspace/CreateWorkspaceDialog";
import { useState } from "react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
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
	Dialog,
	DialogContent,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
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
	orgs = [],
}: {
	user: {
		name: string;
		email: string;
		avatar: string;
	};
	workspaces?: Workspace[];
	orgs?: Organization[];
}) {
	const { isMobile } = useSidebar();
	const navigate = useNavigate();
	const { logout } = useAuth();
	const { activeOrgId, activeWorkspaceId, setActiveOrg, setActiveWorkspace } =
		useOrgWorkspace();
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
	const [switchContextOpen, setSwitchContextOpen] = useState(false);

	const handleOrgSwitch = (orgId: bigint) => {
		if (orgId === activeOrgId) return;
		setActiveOrg(orgId);
	};

	const handleWorkspaceSwitch = (workspaceId: bigint) => {
		setActiveWorkspace(workspaceId);
	};

	const handleCreateOrgSuccess = (orgId: bigint) => {
		// Switch to the new org
		setActiveOrg(orgId);
	};

	const handleCreateWorkspaceSuccess = (workspaceId: bigint) => {
		// Switch to the new workspace
		setActiveWorkspace(workspaceId);
	};

	return (
		<SidebarMenu>
			<SidebarMenuItem>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<SidebarMenuButton
							size="lg"
							className="data-[state=open]:bg-sidebar-accent/90 data-[state=open]:border-2 data-[state=open]:border-black dark:data-[state=open]:border-neutral-700 data-[state=open]:shadow-[2px_2px_0px_0px_#000] cursor-pointer"
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

						{/* Current Organization & Workspace */}
						{activeOrg && (
							<div className="px-2 py-3 space-y-2">
								<div className="space-y-2">
									<div className="text-xs font-semibold text-muted-foreground">
										Organization
									</div>
									<button
										onClick={() => setSwitchContextOpen(true)}
										className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-md bg-secondary hover:bg-secondary/80 transition-colors text-sm font-medium"
									>
										<div className="flex items-center gap-2 min-w-0">
											<Building2 className="size-4 shrink-0" />
											<span className="truncate">{activeOrg.name}</span>
										</div>
										<ChevronsUpDown className="size-3 shrink-0 opacity-50" />
									</button>
								</div>
								{workspaces.length > 0 && (
									<div className="space-y-2">
										<div className="text-xs font-semibold text-muted-foreground">
											Workspace
										</div>
										<button
											onClick={() => setSwitchContextOpen(true)}
											className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-md bg-secondary hover:bg-secondary/80 transition-colors text-sm font-medium"
										>
											<span className="truncate">
												{workspaces.find((w) => w.id === activeWorkspaceId)
													?.name || "Select workspace"}
											</span>
											<ChevronsUpDown className="size-3 shrink-0 opacity-50" />
										</button>
									</div>
								)}
							</div>
						)}
						<DropdownMenuSeparator />

						<DropdownMenuGroup>
							<DropdownMenuItem
								onClick={() => navigate("/profile")}
								className="cursor-pointer"
							>
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

			{/* Context Switch Dialog */}
			<Dialog open={switchContextOpen} onOpenChange={setSwitchContextOpen}>
				<DialogContent className="max-w-2xl">
					<DialogHeader>
						<DialogTitle>Switch Context</DialogTitle>
					</DialogHeader>
					<div className="grid grid-cols-2 gap-4">
						{/* Left side - Organizations */}
						<div className="space-y-2 border-r pr-4">
							<div className="text-sm font-semibold">Organizations</div>
							<div className="space-y-1 overflow-y-auto max-h-64">
								{orgs.map((org) => (
									<button
										key={org.id.toString()}
										onClick={() => handleOrgSwitch(org.id)}
										className={`w-full text-left px-3 py-2 rounded-md transition-colors flex items-center gap-2 ${
											activeOrgId === org.id
												? "bg-primary text-primary-foreground"
												: "hover:bg-secondary"
										}`}
									>
										<Building2 className="size-4 shrink-0" />
										<span className="truncate">{org.name}</span>
										{activeOrgId === org.id && (
											<Check className="size-3 ml-auto shrink-0" />
										)}
									</button>
								))}
								<button
									onClick={() => setCreateOrgOpen(true)}
									className="w-full text-left px-3 py-2 rounded-md hover:bg-secondary transition-colors flex items-center gap-2 text-primary mt-2 pt-2 border-t"
								>
									<Plus className="size-4" />
									<span>Create Organization</span>
								</button>
							</div>
						</div>

						{/* Right side - Workspaces */}
						<div className="space-y-2 pl-4">
							<div className="text-sm font-semibold">Workspaces</div>
							<div className="space-y-1 overflow-y-auto max-h-64">
								{workspaces.length > 0 ? (
									<>
										{workspaces.map((workspace) => (
											<button
												key={workspace.id.toString()}
												onClick={() => handleWorkspaceSwitch(workspace.id)}
												className={`w-full text-left px-3 py-2 rounded-md transition-colors flex items-center justify-between ${
													activeWorkspaceId === workspace.id
														? "bg-primary text-primary-foreground"
														: "hover:bg-secondary"
												}`}
											>
												<span className="truncate">{workspace.name}</span>
												{activeWorkspaceId === workspace.id && (
													<Check className="size-3 shrink-0" />
												)}
											</button>
										))}
										<button
											onClick={() => setCreateWorkspaceOpen(true)}
											className="w-full text-left px-3 py-2 rounded-md hover:bg-secondary transition-colors flex items-center gap-2 text-primary mt-2 pt-2 border-t"
										>
											<Plus className="size-4" />
											<span>Create Workspace</span>
										</button>
									</>
								) : (
									<div className="text-sm text-muted-foreground py-4">
										No workspaces in this organization
									</div>
								)}
							</div>
						</div>
					</div>
					<div className="flex justify-end gap-2 pt-4">
						<Button
							variant="outline"
							onClick={() => setSwitchContextOpen(false)}
						>
							Done
						</Button>
					</div>
				</DialogContent>
			</Dialog>
		</SidebarMenu>
	);
}
