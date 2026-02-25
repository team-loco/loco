import { useAuth } from "@/auth/AuthProvider";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { createOrg } from "@/gen/loco/org/v1";
import type { Organization } from "@/gen/loco/org/v1/org_pb";
import { whoAmI } from "@/gen/loco/user/v1";
import { createWorkspace } from "@/gen/loco/workspace/v1";
import type { Workspace } from "@/gen/loco/workspace/v1/workspace_pb";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { useTheme } from "@/lib/use-theme";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
	Bell,
	Building2,
	Check,
	ChevronDown,
	Database,
	Edit,
	HardDrive,
	HelpCircle,
	Layers,
	Loader2,
	LogOut,
	Mail,
	Plus,
	Server,
	Settings,
	Zap,
} from "lucide-react";
import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { toast } from "sonner";
import "./layout/ThemeToggle.css";

const RESOURCE_TYPES = [
	{
		value: "SERVICE",
		label: "Service",
		icon: Server,
		available: true,
		color: "text-blue-600",
	},
	{
		value: "DATABASE",
		label: "Database",
		icon: Database,
		available: false,
		color: "text-orange-600",
	},
	{
		value: "FUNCTION",
		label: "Function",
		icon: Zap,
		available: false,
		color: "text-yellow-600",
	},
	{
		value: "CACHE",
		label: "Cache",
		icon: Layers,
		available: false,
		color: "text-purple-600",
	},
	{
		value: "QUEUE",
		label: "Queue",
		icon: Mail,
		available: false,
		color: "text-pink-600",
	},
	{
		value: "BLOB",
		label: "Blob Storage",
		icon: HardDrive,
		available: false,
		color: "text-green-600",
	},
];

export function SiteHeader() {
	const navigate = useNavigate();
	const location = useLocation();
	const {
		activeOrgId,
		activeWorkspaceId,
		orgs,
		workspaces,
		setActiveOrg,
		setActiveWorkspace,
		addOrg,
		addWorkspace,
	} = useOrgWorkspace();
	const { logout } = useAuth();
	const { data: whoAmIResponse } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;
	const { theme, toggleTheme } = useTheme();
	const [dropdownOpen, setDropdownOpen] = useState(false);
	const [userDropdownOpen, setUserDropdownOpen] = useState(false);
	const [switchContextOpen, setSwitchContextOpen] = useState(false);
	const [showCreateOrgForm, setShowCreateOrgForm] = useState(false);
	const [showCreateWorkspaceForm, setShowCreateWorkspaceForm] = useState(false);
	const [newOrgName, setNewOrgName] = useState("");
	const [newWorkspaceName, setNewWorkspaceName] = useState("");
	const [newWorkspaceDescription, setNewWorkspaceDescription] = useState("");
	const [pendingOrgId, setPendingOrgId] = useState<string | null>(null);
	const [pendingWorkspaceId, setPendingWorkspaceId] = useState<string | null>(
		null,
	);

	const { mutate: mutateCreateOrg, isPending: isCreatingOrg } =
		useMutation(createOrg);
	const { mutate: mutateCreateWorkspace, isPending: isCreatingWorkspace } =
		useMutation(createWorkspace);

	const activeOrg = orgs.find((org) => org.id === activeOrgId);

	const playSound = async (isDark: boolean) => {
		new window.AudioContext();
		const audio = new Audio(`${isDark ? "/lightMode.wav" : "/darkMode.wav"}`);
		audio.volume = 0.9;
		await audio.play();
	};

	const handleThemeToggle = async () => {
		const isDark = theme === "dark";
		toggleTheme();
		await playSound(isDark);
	};

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
						addOrg({
							id: newOrgId,
							name: newOrgName,
						} as Organization);
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
						addWorkspace({
							id: newWorkspaceId,
							orgId: activeOrgId!,
							name: newWorkspaceName,
							description: newWorkspaceDescription,
						} as Workspace);
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

	const getNavItems = useCallback(
		() => [
			{
				title: "Dashboard",
				url:
					activeOrgId && activeWorkspaceId
						? `/org/${activeOrgId}/wks/${activeWorkspaceId}/dashboard`
						: "/dashboard",
			},
			{
				title: "Observability",
				url:
					activeOrgId && activeWorkspaceId
						? `/org/${activeOrgId}/wks/${activeWorkspaceId}/observability`
						: "/observability",
			},
			{
				title: "Events",
				url:
					activeOrgId && activeWorkspaceId
						? `/org/${activeOrgId}/wks/${activeWorkspaceId}/events`
						: "/events",
			},
			{
				title: "Usage",
				url:
					activeOrgId && activeWorkspaceId
						? `/org/${activeOrgId}/wks/${activeWorkspaceId}/usage`
						: "/usage",
			},
			{
				title: "Tokens",
				url: "/tokens",
			},
			{
				title: "Team",
				url: "/team",
			},
		],
		[activeOrgId, activeWorkspaceId],
	);

	const isActive = useCallback(
		(url: string) => {
			if (location.pathname === url) return true;
			if (location.pathname.startsWith(url + "/")) {
				if (url.endsWith("/dashboard")) return false;
				return true;
			}
			return false;
		},
		[location.pathname],
	);

	const navContainerRef = useRef<HTMLDivElement>(null);
	const buttonRefs = useRef<{ [key: string]: HTMLButtonElement | null }>({});
	const [sliderStyle, setSliderStyle] = useState<{
		width: number;
		left: number;
	} | null>(null);

	const calculateSliderStyle = useCallback(() => {
		if (!navContainerRef.current) return;

		const container = navContainerRef.current;
		let activeButton: HTMLButtonElement | null = null;

		for (const item of getNavItems()) {
			if (isActive(item.url) && buttonRefs.current[item.url]) {
				activeButton = buttonRefs.current[item.url];
				break;
			}
		}

		if (activeButton) {
			const activeRect = activeButton.getBoundingClientRect();
			const containerRect = container.getBoundingClientRect();
			setSliderStyle({
				width: activeRect.width,
				left: activeRect.left - containerRect.left,
			});
		}
	}, [getNavItems, isActive]);

	useLayoutEffect(() => {
		calculateSliderStyle();

		const handleResize = () => {
			calculateSliderStyle();
		};

		window.addEventListener("resize", handleResize);
		return () => window.removeEventListener("resize", handleResize);
	}, [calculateSliderStyle]);

	return (
		<>
			<header className="fixed top-0 left-0 right-0 z-40 flex w-full items-center border-b border-border/50 bg-background/95 backdrop-blur-sm">
				<div className="flex h-11 w-full items-center px-6">
					{/* Inline Navigation - Evenly Spaced */}
					<div
						className="flex-1 flex items-center justify-center gap-2 relative"
						ref={navContainerRef}
					>
						{/* Sliding background */}
						{sliderStyle && (
							<div
								className="absolute h-8 bg-primary rounded-sm transition-all duration-300 ease-out"
								style={{
									width: `${sliderStyle.width}px`,
									left: `${sliderStyle.left}px`,
								}}
							/>
						)}

						{getNavItems().map((item) => (
							<Button
								key={item.title}
								ref={(el) => {
									if (el) buttonRefs.current[item.url] = el;
								}}
								variant="ghost"
								size="sm"
								onClick={() => navigate(item.url)}
								className={`h-8 px-3 text-sm rounded-none relative z-10 hover:bg-transparent ${
									isActive(item.url) ? "text-primary-foreground" : ""
								}`}
							>
								{item.title}
							</Button>
						))}
					</div>

					<div className="ml-auto inline-flex items-center justify-end gap-3">
						{/* User Avatar Dropdown */}
						<DropdownMenu
							open={userDropdownOpen}
							onOpenChange={setUserDropdownOpen}
						>
							<DropdownMenuTrigger asChild>
								<Button variant="ghost" size="icon" className="h-8 w-8">
									<Avatar className="h-6 w-6">
										<AvatarImage src={user?.avatarUrl} alt={user?.name} />
										<AvatarFallback className="text-xs">
											{user?.name?.charAt(0)?.toUpperCase()}
										</AvatarFallback>
									</Avatar>
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent
								className="w-[--radix-dropdown-menu-trigger-width] min-w-56 rounded-lg"
								side="bottom"
								align="end"
								sideOffset={4}
							>
								<DropdownMenuLabel className="p-0 font-normal">
									<a
										href="/profile"
										className="flex items-center justify-between gap-3 px-2 py-2 text-left rounded hover:bg-accent transition-colors"
									>
										<div className="flex-1 min-w-0">
											<div className="text-sm font-semibold text-black truncate">
												{user?.name}
											</div>
											<div className="text-xs text-black truncate">
												{user?.email}
											</div>
										</div>
										<Settings className="size-4 shrink-0 text-foreground/60" />
									</a>
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
												className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-md bg-accent/20 hover:bg-accent/30 active:bg-accent/40 transition-all duration-150 text-sm font-medium cursor-pointer border border-accent/30"
											>
												<span className="truncate font-semibold">
													{activeOrg.name}
												</span>
												<Edit className="size-3 shrink-0" />
											</button>
										</div>
										{workspaces.length > 0 && (
											<div className="space-y-2">
												<div className="text-xs font-semibold text-muted-foreground">
													Workspace
												</div>
												<button
													onClick={() => setSwitchContextOpen(true)}
													className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-md bg-accent/20 hover:bg-accent/30 active:bg-accent/40 transition-all duration-150 text-sm font-medium cursor-pointer border border-accent/30"
												>
													<span className="truncate font-semibold">
														{workspaces.find((w) => w.id === activeWorkspaceId)
															?.name || "Select workspace"}
													</span>
													<Edit className="size-3 shrink-0" />
												</button>
											</div>
										)}
									</div>
								)}

								<DropdownMenuSeparator />
								<DropdownMenuGroup>
									<DropdownMenuItem className="cursor-pointer flex justify-between">
										<span>Notifications</span>
										<Bell className="size-4" />
									</DropdownMenuItem>
									<DropdownMenuItem className="cursor-pointer flex justify-between">
										<span>Support</span>
										<HelpCircle className="size-4" />
									</DropdownMenuItem>
									<DropdownMenuItem
										onClick={(e) => {
											e.preventDefault();
											void handleThemeToggle();
										}}
										onSelect={(e) => e.preventDefault()}
										className="cursor-pointer flex justify-between"
									>
										<span>{theme === "dark" ? "Light Mode" : "Dark Mode"}</span>
										<div className="text-foreground/60">
											{theme === "dark" ? (
												<div className="div-toggle-btn-dark border-0 shadow-none size-4"></div>
											) : (
												<div className="div-toggle-btn-light border-0 shadow-none size-4"></div>
											)}
										</div>
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
									className="cursor-pointer flex justify-between"
								>
									<span>Log out</span>
									<LogOut className="size-4" />
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					</div>
				</div>
			</header>

			{/* Context Switch Dialog */}
			<Dialog
				open={switchContextOpen}
				onOpenChange={(newOpen) => {
					if (!newOpen) {
						// Reset form state on close
						setShowCreateOrgForm(false);
						setShowCreateWorkspaceForm(false);
						setNewOrgName("");
						setNewWorkspaceName("");
						setNewWorkspaceDescription("");
					}
					setSwitchContextOpen(newOpen);
				}}
			>
				<DialogContent className="max-w-xl">
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
									onChange={(e) => setNewOrgName(e.target.value)}
									disabled={isCreatingOrg}
									autoFocus
									className="max-w-sm px-3 py-2 border rounded-sm bg-background"
								/>
							</div>
							<div className="flex gap-2 justify-end">
								<Button
									type="button"
									variant="outline"
									onClick={() => setShowCreateOrgForm(false)}
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
									onChange={(e) => setNewWorkspaceName(e.target.value)}
									disabled={isCreatingWorkspace}
									autoFocus
									className="max-w-sm px-3 py-2 border rounded-sm bg-background"
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
									onChange={(e) => setNewWorkspaceDescription(e.target.value)}
									disabled={isCreatingWorkspace}
									rows={3}
									className="max-w-sm px-3 py-2 border rounded-sm bg-background"
								/>
							</div>
							<div className="flex gap-2 justify-end">
								<Button
									type="button"
									variant="outline"
									onClick={() => setShowCreateWorkspaceForm(false)}
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
												key={org.id.toString()}
												onClick={() => handleOrgSwitch(org.id)}
												className={`w-full text-left px-3 py-2 rounded-sm transition-colors flex items-center gap-2 ${
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
											onClick={() => setShowCreateOrgForm(true)}
											className="w-full text-left px-3 py-2 rounded-sm hover:bg-secondary transition-colors flex items-center gap-2 text-primary mt-2 pt-2 border-t cursor-pointer"
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
														className={`w-full text-left px-3 py-2 rounded-sm transition-colors flex items-center justify-between ${
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
													onClick={() => setShowCreateWorkspaceForm(true)}
													className="w-full text-left px-3 py-2 rounded-sm hover:bg-secondary transition-colors flex items-center gap-2 text-primary mt-2 pt-2 border-t cursor-pointer"
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
		</>
	);
}
