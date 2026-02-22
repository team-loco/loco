import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useSidebar } from "@/components/ui/sidebar";
import { useOrgWorkspace } from "@/context/ContextProvider";
import {
	ChevronDown,
	Database,
	HardDrive,
	Layers,
	Mail,
	PanelLeftCloseIcon,
	PanelLeftIcon,
	Server,
	Zap,
} from "lucide-react";
import { useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router";

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
	const location = useLocation();
	const navigate = useNavigate();
	const { open, toggleSidebar } = useSidebar();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const [dropdownOpen, setDropdownOpen] = useState(false);

	// Find the active nav item based on current path
	const pageTitle = useMemo(() => {
		const path = location.pathname;

		// Special cases for settings pages
		if (path.includes("/settings")) {
			if (path.includes("/resource/") && path.includes("/settings")) {
				return "Resource Settings";
			}
			if (path.includes("/wks/") && path.includes("/settings")) {
				return "Workspace Settings";
			}
			if (path.includes("/org/") && path.includes("/settings")) {
				return "Organization Settings";
			}
			return "Settings";
		}

		if (path.includes("/profile")) {
			return "Profile";
		}

		// Check for org-level routes FIRST (before workspace routes)
		if (path.includes("/tokens")) {
			return "Tokens";
		}
		if (path.includes("/team")) {
			return "Team";
		}
		if (path.includes("/organizations")) {
			return "Organizations";
		}

		if (path.includes("/create-resource")) {
			return "Create Resource";
		}

		// Special case for resource details page - show "Resources"
		if (path.includes("/resource/") && !path.includes("/settings")) {
			return "Resources";
		}

		// Check for workspace-scoped routes
		if (path.includes("/wks/")) {
			if (
				path.includes("/dashboard") ||
				path.endsWith("/wks/" + activeWorkspaceId?.toString())
			) {
				return "Dashboard";
			}
			if (path.includes("/resources")) {
				return "Resources";
			}
			if (path.includes("/observability")) {
				return "Observability";
			}
			if (path.includes("/events")) {
				return "Events";
			}
			if (path.includes("/usage")) {
				return "Usage";
			}
		}

		return "Dashboard";
	}, [location.pathname, activeWorkspaceId]);

	return (
		<header className="fixed top-0 left-0 right-0 z-40 flex w-full items-center border-b border-border bg-header-bg">
			<div className="flex h-11 w-full items-center gap-3 px-6">
				<Button
					variant="ghost"
					size="icon"
					onClick={toggleSidebar}
					className={`h-8 w-8 transition-all duration-75 ${
						open ? "bg-accent text-accent-foreground" : ""
					}`}
					aria-label="Toggle Sidebar"
				>
					{open ? (
						<PanelLeftCloseIcon className="h-4 w-4" />
					) : (
						<PanelLeftIcon className="h-4 w-4" />
					)}
				</Button>
				<h1 className="text-lg font-semibold text-foreground">{pageTitle}</h1>
				<div className="ml-auto inline-flex items-center justify-center rounded-md shadow-sm btn-gradient-border">
					<Button
						className="rounded-r-none border-r border-[#404040] h-8 bg-[#1e1e1e]/85 px-3 text-accent font-medium shadow-none hover:shadow-none active:shadow-none backdrop-blur-sm"
						onClick={() => setDropdownOpen(true)}
					>
						New Service
					</Button>
					<DropdownMenu open={dropdownOpen} onOpenChange={setDropdownOpen}>
						<DropdownMenuTrigger asChild>
							<Button
								size="icon"
								className="h-8 w-9 rounded-l-none bg-[#1e1e1e]/85 text-[#faf9f6] shadow-none hover:shadow-none active:shadow-none focus-visible:ring-0 focus-visible:ring-offset-0 backdrop-blur-sm"
							>
								<ChevronDown
									className={`h-4 w-4 transition-transform duration-200 ${dropdownOpen ? "rotate-180" : ""}`}
								/>
								<span className="sr-only">Toggle menu</span>
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="w-48">
							{RESOURCE_TYPES.map((type) => {
								const Icon = type.icon;
								return (
									<DropdownMenuItem
										key={type.value}
										onClick={() => {
											if (activeOrgId && activeWorkspaceId) {
												navigate(
													`/org/${activeOrgId}/wks/${activeWorkspaceId}/create-resource?type=${type.value}`,
												);
											}
										}}
										disabled={!type.available}
										className="cursor-pointer"
									>
										<Icon className={`h-4 w-4 mr-2 ${type.color}`} />
										<span>{type.label}</span>
									</DropdownMenuItem>
								);
							})}
						</DropdownMenuContent>
					</DropdownMenu>
				</div>
			</div>
		</header>
	);
}
