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
	Loader2,
} from "lucide-react";
import { useNavigate } from "react-router";
import { toastConnectError, getErrorMessage } from "@/lib/error-handler";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { createOrg, listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { createWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { createConnectQueryKey, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/design/Button";
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
} from "@/components/design/Dialog";
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

export function NavUser({
	user,
}: {
	user: {
		name: string;
		email: string;
		avatar: string;
	};
}) {
	const { isMobile } = useSidebar();
	const navigate = useNavigate();
	const { logout } = useAuth();
	const {
		activeOrgId,
		activeWorkspaceId,
		orgs,
		workspaces,
		setActiveOrg,
		setActiveWorkspace,
	} = useOrgWorkspace();
	const queryClient = useQueryClient();
	const { theme, toggleTheme } = useTheme();

	const activeOrg = orgs.find((org) => org.id === activeOrgId);

	const playSound = async (isDark: boolean) => {
		new window.AudioContext(); // necessary fix audio delay on Safari
		const audio = new Audio(isDark ? "/lightMode.wav" : "/darkMode.wav");
		audio.volume = 0.9;
		await audio.play();
	};

	const handleThemeToggle = async () => {
		const isDark = theme === "dark";
		toggleTheme();
		await playSound(isDark);
	};

	// Dialog state
	const [switchContextOpen, setSwitchContextOpen] = useState(false);
	const [showCreateOrgForm, setShowCreateOrgForm] = useState(false);
	const [showCreateWorkspaceForm, setShowCreateWorkspaceForm] = useState(false);
	const [newOrgName, setNewOrgName] = useState("");
	const [newWorkspaceName, setNewWorkspaceName] = useState("");
	const [newWorkspaceDescription, setNewWorkspaceDescription] = useState("");
	const [pendingOrgId, setPendingOrgId] = useState<string | null>(null);
	const [pendingWorkspaceId, setPendingWorkspaceId] = useState<string | null>(null);

	const { mutate: mutateCreateOrg, isPending: isCreatingOrg } =
		useMutation(createOrg);
	const { mutate: mutateCreateWorkspace, isPending: isCreatingWorkspace } =
		useMutation(createWorkspace);

	const handleOrgSwitch = (orgId: string) => {
		if (orgId === activeOrgId) return;
		setActiveOrg(orgId);
	};

	const handleWorkspaceSwitch = (workspaceId: string) => {
		setActiveWorkspace(workspaceId);
	};

	const handleCreateOrg = (e: React.FormEvent) => {
		e.preventDefault();

		if (!newOrgName.trim()) {
			toast.error("Organization name is required");
			return;
		}

		mutateCreateOrg(
			{ name: newOrgName.trim() },
			{
				onSuccess: (response) => {
					const newOrgId = response.orgId;
					if (newOrgId) {
						toast.success(`Organization "${newOrgName}" created`);
						void queryClient.invalidateQueries({
							queryKey: createConnectQueryKey({
								schema: listUserOrgs,
								cardinality: undefined,
							}),
						});
						// Store as pending - will switch when user clicks Done
						setPendingOrgId(newOrgId);
						setNewOrgName("");
						setShowCreateOrgForm(false);
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to create organization"));
				},
			},
		);
	};

	const handleCreateWorkspace = (e: React.FormEvent) => {
		e.preventDefault();

		if (!newWorkspaceName.trim() || !activeOrgId) {
			toast.error("Workspace name is required");
			return;
		}

		mutateCreateWorkspace(
			{
				orgId: activeOrgId,
				name: newWorkspaceName.trim(),
				description: newWorkspaceDescription.trim() || undefined,
			},
			{
				onSuccess: (response) => {
					const newWorkspaceId = response.workspaceId;
					if (newWorkspaceId) {
						toast.success(`Workspace "${newWorkspaceName}" created`);
						void queryClient.invalidateQueries({
							queryKey: createConnectQueryKey({
								schema: listOrgWorkspaces,
								cardinality: undefined,
							}),
						});
						// Store as pending - will switch when user clicks Done
						setPendingWorkspaceId(newWorkspaceId);
						setNewWorkspaceName("");
						setNewWorkspaceDescription("");
						setShowCreateWorkspaceForm(false);
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to create workspace"));
				},
			},
		);
	};

	return (
		<SidebarMenu>
			<SidebarMenuItem>
				<DropdownMenu>
					<DropdownMenuTrigger
						render={
							<SidebarMenuButton
								size="lg"
								className="data-[state=open]:bg-sidebar-accent/10 cursor-pointer"
							/>
						}
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
					</DropdownMenuTrigger>
					<DropdownMenuContent
						className="w-[--radix-dropdown-menu-trigger-width] min-w-56 rounded-lg"
						side={isMobile ? "bottom" : "right"}
						align="end"
						sideOffset={4}
					>
						<DropdownMenuGroup>
							<DropdownMenuLabel className="p-0 font-normal">
								<div className="px-1 py-1.5 text-left text-sm">
									<span className="truncate font-bold">{user.name}</span>
								</div>
							</DropdownMenuLabel>
						</DropdownMenuGroup>
						<DropdownMenuSeparator />

						{/* Current Organization & Workspace */}
						{activeOrg && (
							<div className="px-2 py-3 space-y-2">
								<div className="space-y-2">
									<div className="text-xs font-semibold text-muted-foreground">
										Organization
									</div>
									<button
										onClick={() => { setSwitchContextOpen(true); }}
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
											onClick={() => { setSwitchContextOpen(true); }}
											className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-md bg-secondary hover:bg-secondary/80 transition-colors text-sm font-medium"
										>
											<span className="truncate">
												{workspaces.find((w) => w.id === activeWorkspaceId)
													?.name ?? "Select workspace"}
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
								onClick={() => {
									void navigate("/profile");
								}}
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
								onSelect={(e) => { e.preventDefault(); }}
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
							onClick={() => {
								void (async () => {
									try {
										await logout();
										void navigate("/");
										toast.success("Logged out successfully");
									} catch (error) {
										toastConnectError(error, "Failed to logout");
									}
								})();
							}}
							className="cursor-pointer"
						>
							<LogOut />
							Log out
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</SidebarMenuItem>

			{/* Context Switch Dialog with Inline Forms */}
			<Dialog open={switchContextOpen} onOpenChange={setSwitchContextOpen}>
				<DialogContent className="max-w-2xl">
					<DialogHeader>
						<DialogTitle>
							{showCreateOrgForm
								? "Create Organization"
								: showCreateWorkspaceForm
									? "Create Workspace"
									: "Switch Context"}
						</DialogTitle>
					</DialogHeader>

					{/* Create Organization Form */}
					{showCreateOrgForm && (
						<form onSubmit={handleCreateOrg} className="space-y-4">
							<div className="space-y-2">
								<label className="text-sm font-medium">Organization Name</label>
								<input
									type="text"
									placeholder="My Organization"
									value={newOrgName}
									onChange={(e) => { setNewOrgName(e.target.value); }}
									disabled={isCreatingOrg}
									autoFocus
									className="w-full px-3 py-2 border rounded-md bg-background"
								/>
							</div>
							<div className="flex gap-2 justify-end">
								<Button
									type="button"
									variant="outline"
									onClick={() => { setShowCreateOrgForm(false); }}
									disabled={isCreatingOrg}
								>
									Back
								</Button>
								<Button
									type="submit"
									disabled={isCreatingOrg || !newOrgName.trim()}
								>
									{isCreatingOrg ? (
										<>
											<Loader2 className="w-4 h-4 mr-2 animate-spin" />
											Creating...
										</>
									) : (
										"Create Organization"
									)}
								</Button>
							</div>
						</form>
					)}

					{/* Create Workspace Form */}
					{showCreateWorkspaceForm && (
						<form onSubmit={handleCreateWorkspace} className="space-y-4">
							<div className="space-y-2">
								<label className="text-sm font-medium">Workspace Name</label>
								<input
									type="text"
									placeholder="Production"
									value={newWorkspaceName}
									onChange={(e) => { setNewWorkspaceName(e.target.value); }}
									disabled={isCreatingWorkspace}
									autoFocus
									className="w-full px-3 py-2 border rounded-md bg-background"
								/>
							</div>
							<div className="space-y-2">
								<label className="text-sm font-medium">
									Description{" "}
									<span className="text-muted-foreground">(optional)</span>
								</label>
								<textarea
									placeholder="Production environment for customer-facing applications"
									value={newWorkspaceDescription}
									onChange={(e) => { setNewWorkspaceDescription(e.target.value); }}
									disabled={isCreatingWorkspace}
									rows={3}
									className="w-full px-3 py-2 border rounded-md bg-background"
								/>
							</div>
							<div className="flex gap-2 justify-end">
								<Button
									type="button"
									variant="outline"
									onClick={() => { setShowCreateWorkspaceForm(false); }}
									disabled={isCreatingWorkspace}
								>
									Back
								</Button>
								<Button
									type="submit"
									disabled={isCreatingWorkspace || !newWorkspaceName.trim()}
								>
									{isCreatingWorkspace ? (
										<>
											<Loader2 className="w-4 h-4 mr-2 animate-spin" />
											Creating...
										</>
									) : (
										"Create Workspace"
									)}
								</Button>
							</div>
						</form>
					)}

					{/* Main Context Switcher View */}
					{!showCreateOrgForm && !showCreateWorkspaceForm && (
						<>
							<div className="grid grid-cols-2 gap-4">
								{/* Left side - Organizations */}
								<div className="space-y-2 border-r pr-4">
									<div className="text-sm font-semibold">Organizations</div>
									<div className="space-y-1 overflow-y-auto max-h-64">
										{orgs.map((org) => (
											<button
												key={org.id}
												onClick={() => { handleOrgSwitch(org.id); }}
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
											onClick={() => { setShowCreateOrgForm(true); }}
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
														key={workspace.id}
														onClick={() => { handleWorkspaceSwitch(workspace.id); }}
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
													onClick={() => { setShowCreateWorkspaceForm(true); }}
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
									onClick={() => {
										// Switch to pending org/workspace if any
										if (pendingOrgId) {
											setActiveOrg(pendingOrgId);
											setPendingOrgId(null);
										} else if (pendingWorkspaceId) {
											setActiveWorkspace(pendingWorkspaceId);
											setPendingWorkspaceId(null);
										}
										setSwitchContextOpen(false);
									}}
								>
									Done
								</Button>
							</div>
						</>
					)}
				</DialogContent>
			</Dialog>
		</SidebarMenu>
	);
}
