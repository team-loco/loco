import { Button } from "@/components/ui/button";
import { useSidebar } from "@/components/ui/sidebar";
import { PanelLeftCloseIcon, PanelLeftIcon, Plus } from "lucide-react";
import { useLocation, useNavigate } from "react-router";
import { useMemo } from "react";

// Navigation items matching AppSidebar structure
const navItems = [
	{ title: "Dashboard", url: "/dashboard" },
	{ title: "Resources", url: "/resources" },
	{ title: "Observability", url: "/observability" },
	{ title: "Events", url: "/events" },
	{ title: "Usage", url: "/usage" },
	{ title: "Tokens", url: "/tokens" },
	{ title: "Team", url: "/team" },
	{ title: "Organizations", url: "/organizations" },
	{ title: "Docs", url: "/docs" },
	{ title: "Packages", url: "/packages" },
];

export function SiteHeader() {
	const location = useLocation();
	const navigate = useNavigate();
	const { open, toggleSidebar } = useSidebar();

	// Find the active nav item based on current path
	const pageTitle = useMemo(() => {
		// Special cases for settings pages
		if (location.pathname.startsWith("/org-settings")) {
			return "Organization Settings";
		}
		if (location.pathname.startsWith("/workspace-settings")) {
			return "Workspace Settings";
		}
		if (location.pathname.startsWith("/resource-settings") || location.pathname.includes("/settings")) {
			return "Settings";
		}
		if (location.pathname.startsWith("/profile")) {
			return "Profile";
		}
		if (location.pathname.startsWith("/create-resource")) {
			return "Create Resource";
		}

		// Special case for resource details page - show "Resources"
		if (location.pathname.startsWith("/resource/")) {
			return "Resources";
		}

		// Find matching nav item
		const activeNavItem = navItems.find((item) => {
			if (item.url === "/dashboard") {
				// Dashboard matches both /dashboard and /home
				return location.pathname === "/dashboard" || location.pathname === "/home";
			}
			return location.pathname.startsWith(item.url);
		});

		return activeNavItem?.title ?? "Dashboard";
	}, [location.pathname]);

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
				<Button
					onClick={() => navigate("/create-resource")}
					className="ml-auto bg-primary hover:bg-primary/90 text-primary-foreground border-2 border-black dark:border-neutral-700 shadow-[2px_2px_0px_0px_#000] hover:shadow-[1px_1px_0px_0px_#000] active:shadow-none active:translate-x-0.5 active:translate-y-0.5 transition-all duration-75 h-8 text-sm"
					size="sm"
				>
					<Plus className="h-4 w-4 mr-2" />
					New Resource
				</Button>
			</div>
		</header>
	);
}
