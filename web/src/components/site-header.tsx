import { Button } from "@/components/ui/button";
import { useSidebar } from "@/components/ui/sidebar";
import { useOrgWorkspace } from "@/context/ContextProvider";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { PanelLeftCloseIcon, PanelLeftIcon, Plus, Server, Database, Zap, Layers, Mail, HardDrive } from "lucide-react";
import { useLocation, useNavigate } from "react-router";
import { useMemo } from "react";

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
			if (path.includes("/dashboard") || path.endsWith("/wks/" + activeWorkspaceId?.toString())) {
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
		<header
			className="fixed top-0 left-0 right-0 z-40 flex w-full items-center border-b border-neutral-300 dark:border-neutral-700 bg-header-bg"
		>
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
				<h1 className="text-lg font-semibold text-foreground">
					{pageTitle}
				</h1>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							className="ml-auto bg-primary hover:bg-primary/90 text-primary-foreground border-2 border-black dark:border-neutral-700 shadow-[2px_2px_0px_0px_#000] hover:shadow-[1px_1px_0px_0px_#000] active:shadow-none active:translate-x-0.5 active:translate-y-0.5 transition-all duration-75 h-8 text-sm"
							size="sm"
						>
							<Plus className="h-4 w-4 mr-2" />
							New Resource
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
											navigate(`/org/${activeOrgId}/wks/${activeWorkspaceId}/create-resource?type=${type.value}`);
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
		</header>
	);
}
